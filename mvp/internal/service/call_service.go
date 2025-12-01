package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"concierge/internal/models"
	"concierge/pkg/asterisk"
	"concierge/pkg/audio"
	"concierge/pkg/yandex"
)

type CallService struct {
	asteriskClient *asterisk.Client
	config         *models.AppConfig
	ctx            context.Context
	cancel         context.CancelFunc
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
	}, nil
}

func (s *CallService) HandleCall(callID, phoneNumber string) {
	log.Printf("[%s] 📞 Звонок на номер: %s\n", callID, phoneNumber)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	yandexClient := yandex.NewClient(s.config.APIKey, s.config.Folder, s.config.Instructions)
	if err := yandexClient.Connect(); err != nil {
		log.Printf("[%s] ❌ Ошибка подключения к Yandex: %v\n", callID, err)
		return
	}
	defer yandexClient.Close()
	log.Printf("[%s] ✅ Подключено к Yandex Realtime API\n", callID)

	log.Printf("[%s] ⏳ Ожидание готовности сессии...\n", callID)
	for !yandexClient.IsSessionReady() {
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("[%s] ✅ Сессия готова\n", callID)

	log.Printf("[%s] 📞 Инициация звонка...\n", callID)
	session, err := s.asteriskClient.MakeCall(ctx, phoneNumber)
	if err != nil {
		log.Printf("[%s] ❌ Ошибка звонка: %v\n", callID, err)
		return
	}

	log.Printf("[%s] ✅ Звонок инициирован, ожидание ответа...\n", callID)

	log.Printf("[%s] ⏳ Ожидание подключения AudioSocket...\n", callID)
	select {
	case <-session.AudioSocketReady:
		log.Printf("[%s] ✅ AudioSocket подключен и готов\n", callID)
	case <-time.After(30 * time.Second):
		log.Printf("[%s] ❌ Таймаут ожидания AudioSocket\n", callID)
		return
	case <-ctx.Done():
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
				log.Printf("[%s] %s", callID, text)

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

		for {
			select {
			case <-ctx.Done():
				return
			case <-session.Done:
				return
			case <-farewellChan:
				farewellReceived = true
				farewellChan = nil
				log.Printf("[%s] 📍 Флаг прощания установлен, отслеживаем response.done...\n", callID)
			case event, ok := <-yandexClient.Events():
				if !ok {
					return
				}

				switch event.Type {
				case "input_audio_buffer.speech_started":
					log.Printf("[%s] 🎤 Речь обнаружена\n", callID)
					speechDetected = true

				case "input_audio_buffer.speech_stopped":
					log.Printf("[%s] 🔇 Речь остановлена\n", callID)

				case "input_audio_buffer.committed":
					if speechDetected {
						log.Printf("[%s] ✅ Аудио буфер зафиксирован, генерируем ответ...\n", callID)
						if err := yandexClient.TriggerResponse("Ответь на реплику собеседника."); err != nil {
							log.Printf("[%s] ⚠️  Ошибка запроса ответа: %v\n", callID, err)
						}
						speechDetected = false
					}

				case "response.created":
					log.Printf("[%s] 🤖 Генерация ответа начата\n", callID)

				case "response.done":
					log.Printf("[%s] ✅ Ответ завершен\n", callID)
					if farewellReceived && !responseDoneSent {
						log.Printf("[%s] 🎯 response.done получен после прощания\n", callID)
						responseDoneSent = true
						close(responseDoneAfterFarewell)
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
		case <-time.After(10 * time.Second):
			log.Printf("[%s] ⚠️  Таймаут ожидания response.done после прощания (10сек)\n", callID)
			return
		}

		select {
		case <-session.AllAudioSent:
			log.Printf("[%s] ⏳ Шаг 3/3: Все аудио отправлено в Asterisk\n", callID)
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
			log.Printf("[%s] ⚠️  Таймаут ожидания AllAudioSent (10сек), завершаем принудительно\n", callID)
		}

		time.Sleep(1 * time.Second)
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

	select {
	case <-ctx.Done():
		log.Printf("[%s] ⚠️  Прерывание по сигналу\n", callID)
		if err := s.asteriskClient.Hangup(session); err != nil {
			log.Printf("[%s] ⚠️  Ошибка завершения звонка: %v\n", callID, err)
		}
	case <-session.Done:
		log.Printf("[%s] 📴 Звонок завершен абонентом\n", callID)
	case <-shouldHangup:
		log.Printf("[%s] 👋 Бот попрощался, завершаем звонок\n", callID)
		if err := s.asteriskClient.Hangup(session); err != nil {
			log.Printf("[%s] ⚠️  Ошибка завершения звонка: %v\n", callID, err)
		}
	case <-time.After(300 * time.Second):
		log.Printf("[%s] ⏱️  Таймаут звонка (5 минут)\n", callID)
		if err := s.asteriskClient.Hangup(session); err != nil {
			log.Printf("[%s] ⚠️  Ошибка завершения звонка: %v\n", callID, err)
		}
	}

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
