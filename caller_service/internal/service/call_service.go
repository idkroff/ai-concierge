package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"concierge/internal/events"
	"concierge/internal/models"
	"concierge/internal/summary"
	"concierge/pkg/asterisk"
	"concierge/pkg/audio"
	"concierge/pkg/yandex"

	"github.com/google/uuid"
)

// Аудио-филлер «секундочку, уточняю», проигрывается собеседнику, пока ждём ответ клиента.
//
//go:embed filler_hold.pcm
var fillerHoldPCM []byte

type CallService struct {
	asteriskClient *asterisk.Client
	config         *models.AppConfig
	ctx            context.Context
	cancel         context.CancelFunc

	mu    sync.Mutex
	calls map[string]*CallControl
}

type CallControl struct {
	answers chan string
}

// convLog копит стенограмму звонка для финальной суммаризации.
// Пишут две горутины (текст ассистента и реплики собеседника) — отсюда мьютекс.
type convLog struct {
	mu      sync.Mutex
	lines   []string
	agentSB strings.Builder
}

func (c *convLog) agentDelta(s string) {
	c.mu.Lock()
	c.agentSB.WriteString(s)
	c.mu.Unlock()
}

func (c *convLog) flushAgent() {
	c.mu.Lock()
	if c.agentSB.Len() > 0 {
		c.lines = append(c.lines, "Ассистент: "+strings.TrimSpace(c.agentSB.String()))
		c.agentSB.Reset()
	}
	c.mu.Unlock()
}

func (c *convLog) callee(s string) {
	c.flushAgent()
	c.mu.Lock()
	c.lines = append(c.lines, "Собеседник: "+s)
	c.mu.Unlock()
}

func (c *convLog) text() string {
	c.flushAgent()
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

func NewCallService(config *models.AppConfig) (*CallService, error) {
	ctx, cancel := context.WithCancel(context.Background())

	asteriskClient := asterisk.NewClient(asterisk.DefaultConfig())
	if err := asteriskClient.Connect(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("ошибка подключения к Asterisk: %w", err)
	}

	log.Println("✅ Asterisk AMI и AudioSocket сервер запущены")

	return &CallService{
		asteriskClient: asteriskClient,
		config:         config,
		ctx:            ctx,
		cancel:         cancel,
		calls:          make(map[string]*CallControl),
	}, nil
}

func (s *CallService) registerCall(callID string) *CallControl {
	cc := &CallControl{answers: make(chan string, 1)}
	s.mu.Lock()
	s.calls[callID] = cc
	s.mu.Unlock()
	return cc
}

func (s *CallService) unregisterCall(callID string) {
	s.mu.Lock()
	delete(s.calls, callID)
	s.mu.Unlock()
}

// DeliverClarification доставляет ответ клиента в живой звонок (не блокирует).
func (s *CallService) DeliverClarification(callID, response string) bool {
	s.mu.Lock()
	cc := s.calls[callID]
	s.mu.Unlock()
	if cc == nil {
		log.Printf("[ws] DeliverClarification: НЕТ живого звонка %s", callID)
		return false
	}
	select {
	case cc.answers <- response:
		log.Printf("[ws] DeliverClarification: доставлено в %s (%q)", callID, response)
		return true
	default:
		log.Printf("[ws] DeliverClarification: канал занят для %s", callID)
		return false
	}
}

func (s *CallService) HandleCall(callID, phoneNumber, userContext string, interactive bool, em events.Emitter) {
	if em == nil {
		em = events.NoopEmitter{}
	}
	log.Printf("[%s] 📞 Звонок на номер: %s (интерактивный: %v)\n", callID, phoneNumber, interactive)
	log.Printf("[%s] 📝 Контекст: %s\n", callID, userContext)
	em.Emit(events.NewCallStarted(callID, phoneNumber))

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	cc := s.registerCall(callID)
	defer s.unregisterCall(callID)

	instructions := s.config.BuildInstructions(userContext)
	if interactive {
		instructions = s.config.BuildInstructionsInteractive(userContext)
	}
	em.Emit(events.NewCallConnecting(callID, "yandex"))
	em.Emit(events.NewYandexConnecting(callID))
	yandexClient := yandex.NewClient(s.config.APIKey, s.config.Folder, instructions, interactive)
	if err := yandexClient.Connect(); err != nil {
		log.Printf("[%s] ❌ Ошибка подключения к Yandex: %v\n", callID, err)
		em.Emit(events.NewCallError(callID, err.Error(), "yandex"))
		return
	}
	defer yandexClient.Close()
	log.Printf("[%s] ✅ Подключено к Yandex Realtime API\n", callID)
	em.Emit(events.NewYandexConnected(callID))

	log.Printf("[%s] ⏳ Ожидание готовности сессии...\n", callID)
	for !yandexClient.IsSessionReady() {
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("[%s] ✅ Сессия готова\n", callID)
	em.Emit(events.NewYandexSessionReady(callID))

	log.Printf("[%s] 📞 Инициация звонка...\n", callID)
	em.Emit(events.NewAsteriskOriginateSent(callID, phoneNumber))
	session, err := s.asteriskClient.MakeCall(ctx, phoneNumber)
	if err != nil {
		log.Printf("[%s] ❌ Ошибка звонка: %v\n", callID, err)
		em.Emit(events.NewCallError(callID, err.Error(), "asterisk"))
		return
	}

	log.Printf("[%s] ✅ Звонок инициирован, ожидание ответа...\n", callID)

	log.Printf("[%s] ⏳ Ожидание подключения AudioSocket...\n", callID)
	select {
	case <-session.AudioSocketReady:
		log.Printf("[%s] ✅ AudioSocket подключен и готов\n", callID)
		em.Emit(events.NewAsteriskAudiosocketReady(callID))
	case <-time.After(30 * time.Second):
		log.Printf("[%s] ❌ Таймаут ожидания AudioSocket\n", callID)
		em.Emit(events.NewCallError(callID, "audiosocket timeout", "asterisk"))
		em.Emit(events.NewCallEnded(callID, "audiosocket_timeout"))
		return
	case <-ctx.Done():
		em.Emit(events.NewCallEnded(callID, "cancelled"))
		return
	}

	resampler8to24 := audio.NewStreamingResampler(8000, 24000)
	resampler44to8 := audio.NewStreamingResampler(44100, 8000)
	defer resampler8to24.Close()
	defer resampler44to8.Close()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		var packetsProcessed int
		for {
			select {
			case <-ctx.Done():
				return
			case <-session.Done:
				return
			case audioData := <-session.AudioInput:
				packetsProcessed++

				resampled, err := resampler8to24.Resample(audioData)
				if err != nil {
					log.Printf("[%s] ⚠️  Ошибка ресемплинга: %v\n", callID, err)
					continue
				}

				if err := yandexClient.SendAudio(resampled); err != nil {
					log.Printf("[%s] ⚠️  Ошибка отправки аудио в Yandex: %v\n", callID, err)
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		firstChunk := true
		for {
			select {
			case <-ctx.Done():
				return
			case <-session.Done:
				return
			case audioData, ok := <-yandexClient.AudioOutput():
				if !ok {
					return
				}

				if len(audioData) > 0 {
					log.Printf("[%s] 🎵 Получен аудио чанк от Yandex: %d байт (%.2f сек)\n",
						callID, len(audioData), float64(len(audioData))/2.0/44100.0)
					em.Emit(events.NewYandexAudioChunk(callID, len(audioData)))
				}

				// Задержка перед первым чанком, иначе Asterisk обрезает начало первого слова.
				if firstChunk {
					firstChunk = false
					time.Sleep(500 * time.Millisecond)
				}

				resampled, err := resampler44to8.Resample(audioData)
				if err != nil {
					log.Printf("[%s] ⚠️  Ошибка ресемплинга ответа: %v\n", callID, err)
					continue
				}

				select {
				case session.AudioOutput <- resampled:
				default:
				}
			}
		}
	}()

	cl := &convLog{} // стенограмма для финального итога

	wg.Add(1)
	shouldHangup := make(chan struct{})
	farewellDetected := make(chan struct{})
	go func() {
		defer wg.Done()
		var fullText string
		var farewellSent bool
		for {
			select {
			case <-ctx.Done():
				if fullText != "" {
					log.Printf("[%s] 📝 Полный ответ: %s\n", callID, fullText)
				}
				return
			case <-session.Done:
				if fullText != "" {
					log.Printf("[%s] 📝 Полный ответ: %s\n", callID, fullText)
				}
				return
			case text, ok := <-yandexClient.TextOutput():
				if !ok {
					return
				}
				fullText += text
				cl.agentDelta(text)
				log.Printf("[%s] %s", callID, text)
				em.Emit(events.NewYandexTextDelta(callID, text))

				if !farewellSent && (strings.Contains(fullText, "[ЗАВЕРШИТЬ]") ||
					strings.Contains(fullText, "До свидания") ||
					strings.Contains(fullText, "Всего доброго")) {
					log.Printf("[%s] 👋 Обнаружено прощание в тексте, ждем завершения генерации аудио...\n", callID)
					farewellSent = true
					close(farewellDetected)
				}
			}
		}
	}()

	wg.Add(1)
	responseDoneAfterFarewell := make(chan struct{})
	go func() {
		defer wg.Done()
		var speechDetected bool
		var farewellReceived bool
		var responseDoneSent bool
		farewellChan := farewellDetected
		functionCallsCh := yandexClient.FunctionCalls()

		// Стейт-машина уточнения. Только эта горутина выпускает response.create.
		const (
			clarIdle           = iota
			clarClarifying     // ask_principal получен, ждём response.done тулзового ответа
			clarAwaitingAnswer // сказали филлер, ждём ответа клиента (до 30с)
			clarResolving      // ответ/таймаут отдан модели, ждём старта ответного response
		)
		clarState := clarIdle
		activeResponse := true
		pendingAction := "" // отложенный response.create, один слот
		var fcCallID, fcClarID, fcQuestion string
		var clarTimer <-chan time.Time
		var fillerTimer <-chan time.Time

		playFiller := func() {
			if len(fillerHoldPCM) == 0 {
				log.Printf("[%s] 🔊 filler: пусто (клип не загружен)", callID)
				return
			}
			buf := make([]byte, len(fillerHoldPCM))
			copy(buf, fillerHoldPCM)
			select {
			case session.AudioOutput <- buf:
				log.Printf("[%s] 🔊 filler -> AudioOutput (%d байт)", callID, len(buf))
			default:
				log.Printf("[%s] ⚠️  filler ПОТЕРЯН (AudioOutput переполнен)", callID)
			}
		}

		// respond выпускает response.create, не допуская двух активных ответов.
		respond := func(instr string) {
			if activeResponse {
				pendingAction = instr
				log.Printf("[%s] ⏸️  respond ОТЛОЖЕН (активен ответ): %.60q", callID, instr)
				return
			}
			activeResponse = true
			log.Printf("[%s] ▶️  respond ВЫПУСК: %.60q", callID, instr)
			if err := yandexClient.TriggerResponse(instr); err != nil {
				log.Printf("[%s] ⚠️  Ошибка response.create: %v\n", callID, err)
			}
		}

		parseQuestion := func(args string) string {
			var p struct {
				Question string `json:"question"`
			}
			if json.Unmarshal([]byte(args), &p) == nil && p.Question != "" {
				return p.Question
			}
			if args != "" {
				return args
			}
			return "Уточните, пожалуйста, как ответить собеседнику."
		}

		// abortOutstanding закрывает незакрытый tool-call при завершении звонка.
		abortOutstanding := func() {
			if clarState != clarIdle && fcCallID != "" {
				_ = yandexClient.SubmitFunctionOutput(fcCallID, "Звонок завершается, уточнение невозможно.")
			}
		}

		for {
			select {
			case <-ctx.Done():
				abortOutstanding()
				return
			case <-session.Done:
				abortOutstanding()
				return
			case <-farewellChan:
				farewellReceived = true
				farewellChan = nil
				log.Printf("[%s] 📍 Флаг прощания установлен, отслеживаем response.done...\n", callID)

			case fc, ok := <-functionCallsCh:
				if !ok {
					functionCallsCh = nil
					continue
				}
				log.Printf("[%s] 🛠️  functionCall id=%s name=%q args=%q (clarState=%d)", callID, fc.CallID, fc.Name, fc.Arguments, clarState)
				// У Yandex call_id — это имя инструмента, а не уникальный id, поэтому дедуп по нему невозможен.
				if clarState != clarIdle {
					// Уже идёт уточнение: текущий вызов НЕ закрываем через function_call_output
					// (совпавший call_id закрыл бы не тот вызов).
					log.Printf("[%s] 🛠️  уже идёт уточнение (clarState=%d) — игнор нового вызова", callID, clarState)
					continue
				}
				fcCallID = fc.CallID
				fcQuestion = parseQuestion(fc.Arguments)
				clarState = clarClarifying
				log.Printf("[%s] ❓ ask_principal: %s → clarState=Clarifying", callID, fcQuestion)
				// Филлер идёт в аудио-канал Asterisk напрямую, минуя Yandex — сессию не трогает.
				playFiller()

			case ans := <-cc.answers:
				if clarState == clarAwaitingAnswer {
					log.Printf("[%s] 💬 Ответ клиента получен\n", callID)
					// Сначала закрываем function_call результатом, затем — response.create.
					if err := yandexClient.SubmitFunctionOutput(fcCallID, ans); err != nil {
						log.Printf("[%s] ⚠️  SubmitFunctionOutput: %v\n", callID, err)
					}
					respond("Клиент уточнил: " + ans + ". Ответь собеседнику по сути, кратко, на русском.")
					em.Emit(events.NewClarificationResolved(callID, fcClarID))
					clarState = clarResolving
					clarTimer, fillerTimer = nil, nil
					fcCallID, fcQuestion, fcClarID = "", "", ""
				}

			case <-fillerTimer:
				if clarState == clarAwaitingAnswer {
					playFiller()
					fillerTimer = time.After(9 * time.Second)
				} else {
					fillerTimer = nil
				}

			case <-clarTimer:
				if clarState == clarAwaitingAnswer {
					log.Printf("[%s] ⏱️  Таймаут уточнения (30с)\n", callID)
					_ = yandexClient.SubmitFunctionOutput(fcCallID, "Клиент не ответил вовремя, информация недоступна.")
					respond("Вежливо извинись, что не получилось уточнить прямо сейчас, и продолжи разговор по сути. Не завершай звонок.")
					em.Emit(events.NewClarificationTimeout(callID, fcClarID))
					clarState = clarResolving
					clarTimer, fillerTimer = nil, nil
					fcCallID, fcQuestion, fcClarID = "", "", ""
				}

			case event, ok := <-yandexClient.Events():
				if !ok {
					log.Printf("[%s] 🔌 Events() закрыт — выходим из events-горутины", callID)
					return
				}

				switch event.Type {
				case "response.output_audio.delta", "response.output_text.delta", "response.function_call_arguments.delta":
					// высокочастотные дельты — не логируем
				default:
					log.Printf("[%s] 🔔 ev=%s (clar=%d active=%v pending=%v)", callID, event.Type, clarState, activeResponse, pendingAction != "")
				}

				switch event.Type {
				case "conversation.item.input_audio_transcription.completed":
					if event.Transcript != "" {
						log.Printf("[%s] 👤 Транскрипция: %s\n", callID, event.Transcript)
						cl.callee(event.Transcript)
						em.Emit(events.NewYandexInputTranscript(callID, event.Transcript))
					}

				case "input_audio_buffer.speech_started":
					log.Printf("[%s] 🎤 Речь обнаружена\n", callID)
					speechDetected = true
					em.Emit(events.NewYandexSpeechStarted(callID))

				case "input_audio_buffer.speech_stopped":
					log.Printf("[%s] 🔇 Речь остановлена\n", callID)
					em.Emit(events.NewYandexSpeechStopped(callID))

				case "input_audio_buffer.committed":
					if speechDetected {
						speechDetected = false
						// Во время уточнения НЕ отвечаем собеседнику сами.
						if clarState == clarIdle {
							log.Printf("[%s] ✅ Аудио буфер зафиксирован, генерируем ответ...\n", callID)
							respond("Ответь на реплику собеседника.")
						} else {
							log.Printf("[%s] ⏸️  Реплика во время уточнения — ответ подавлен\n", callID)
						}
					}

				case "response.created":
					activeResponse = true
					if clarState == clarResolving {
						clarState = clarIdle
						log.Printf("[%s] ↩️  clarState=Resolving→Idle (ответный response стартовал)", callID)
					}
					log.Printf("[%s] 🤖 Генерация ответа начата\n", callID)

				case "response.done":
					activeResponse = false
					cl.flushAgent()
					log.Printf("[%s] ✅ Ответ завершен\n", callID)
					em.Emit(events.NewYandexResponseDone(callID))
					if farewellReceived && !responseDoneSent {
						log.Printf("[%s] 🎯 response.done получен после прощания\n", callID)
						responseDoneSent = true
						close(responseDoneAfterFarewell)
					}

					if clarState == clarClarifying {
						// НЕ выпускаем свой response.create: пока function_call не закрыт
						// через function_call_output, любой response.create обрывает сессию Yandex.
						fcClarID = uuid.New().String()
						em.Emit(events.NewClarificationRequest(callID, fcClarID, fcQuestion))
						clarTimer = time.After(30 * time.Second)
						fillerTimer = time.After(9 * time.Second)
						clarState = clarAwaitingAnswer
						log.Printf("[%s] 📨 Запрос уточнения '%s' (clar=%s) отправлен; clarState=AwaitingAnswer, таймер 30с\n", callID, fcQuestion, fcClarID)
					} else if pendingAction != "" {
						a := pendingAction
						pendingAction = ""
						activeResponse = true
						log.Printf("[%s] ▶️  выпуск отложенного respond: %.60q", callID, a)
						if err := yandexClient.TriggerResponse(a); err != nil {
							log.Printf("[%s] ⚠️  Ошибка отложенного response.create: %v\n", callID, err)
						}
					}
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		select {
		case <-farewellDetected:
			log.Printf("[%s] ⏳ Шаг 1/3: Прощание обнаружено\n", callID)
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Second):
			log.Printf("[%s] ⚠️  Таймаут ожидания прощания\n", callID)
			return
		}

		select {
		case <-responseDoneAfterFarewell:
			log.Printf("[%s] ⏳ Шаг 2/3: Генерация аудио завершена\n", callID)
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			log.Printf("[%s] ⚠️  Таймаут ожидания response.done после прощания (3сек)\n", callID)
			return
		}

		// Сигнал Producer-у, что данных больше не будет — он сольёт остатки и даст Consumer-у штатно закрыть AllAudioSent.
		session.SignalAudioOutputDone()

		select {
		case <-session.AllAudioSent:
			log.Printf("[%s] ⏳ Шаг 3/3: Все аудио отправлено в Asterisk\n", callID)
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			log.Printf("[%s] ⚠️  Таймаут ожидания AllAudioSent (3сек), завершаем принудительно\n", callID)
		}

		time.Sleep(500 * time.Millisecond)
		log.Printf("[%s] 👋 Все условия выполнены, завершаем звонок\n", callID)

		select {
		case shouldHangup <- struct{}{}:
		default:
		}
	}()

	log.Printf("[%s] 🤖 Отправляем команду приветствия...\n", callID)
	if err := yandexClient.TriggerResponse("Поздоровайся и начни разговор."); err != nil {
		log.Printf("[%s] ⚠️  Ошибка отправки приветствия: %v\n", callID, err)
	}

	var endReason string
	select {
	case <-ctx.Done():
		endReason = "cancelled"
		log.Printf("[%s] ⚠️  Прерывание по сигналу\n", callID)
	case <-session.Done:
		endReason = "abonent_hangup"
		log.Printf("[%s] 📴 Звонок завершен абонентом\n", callID)
	case <-shouldHangup:
		endReason = "farewell"
		log.Printf("[%s] 👋 Бот попрощался, завершаем звонок\n", callID)
	case <-time.After(300 * time.Second):
		endReason = "timeout"
		log.Printf("[%s] ⏱️  Таймаут звонка (5 минут)\n", callID)
	}
	em.Emit(events.NewAsteriskHangup(callID))
	if err := s.asteriskClient.Hangup(session); err != nil {
		log.Printf("[%s] ⚠️  Ошибка завершения звонка: %v\n", callID, err)
	}

	// Краткий итог звонка для пользователя (на свежем ctx — ctx звонка уже отменён).
	if transcript := cl.text(); transcript != "" {
		sumCtx, sumCancel := context.WithTimeout(context.Background(), 12*time.Second)
		status, summaryText, err := summary.Generate(sumCtx, s.config.APIKey, s.config.Folder, userContext, transcript)
		sumCancel()
		if err != nil {
			log.Printf("[%s] ⚠️  Суммаризация не удалась: %v\n", callID, err)
		} else if summaryText != "" {
			log.Printf("[%s] 📋 Итог [%s]: %s\n", callID, status, summaryText)
			em.Emit(events.NewCallSummary(callID, status, summaryText))
		}
	}

	em.Emit(events.NewCallEnded(callID, endReason))

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("[%s] ⚠️  Таймаут ожидания завершения goroutine\n", callID)
	}

	log.Printf("[%s] ✅ Звонок завершен\n", callID)
}

func (s *CallService) Close() error {
	s.cancel()
	if s.asteriskClient != nil {
		return s.asteriskClient.Close()
	}
	return nil
}
