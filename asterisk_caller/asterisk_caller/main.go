package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/CyCoreSystems/ari/v6"
	"github.com/CyCoreSystems/ari/v6/client/native"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

const (
	ARIHost                  = "http://localhost:8088/ari"
	ARIUser                  = "goari"
	ARIPassword              = "goaripass123"
	ARIApp                   = "concierge"
	SoundsDir                = "./sounds"
	OutputSound              = "output"
	RecordingsDir            = "./recordings"
	RecordingName            = "call-recording"
	WebSocketPort            = ":8089"
	AudioSocketPort          = ":9092"
	AudioSocketChunkDuration = 1 * time.Second // Батчи по 1 секунде
)

var (
	wsUpgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Разрешаем все источники
		},
	}
	wsClients   = make(map[*websocket.Conn]bool)
	wsClientsMu sync.Mutex
	wsBroadcast = make(chan []byte, 100)
)

func main() {
	_ = godotenv.Load("../.env")

	phoneNumber := flag.String("phone", "", "Номер телефона для звонка")
	audioFile := flag.String("audio", "../input.mp3", "Аудио файл для воспроизведения")
	enableWS := flag.Bool("ws", false, "Включить WebSocket сервер для трансляции аудио")

	flag.Parse()

	if *phoneNumber == "" {
		log.Fatal("❌ Укажите номер телефона через флаг -phone")
	}

	fmt.Println("☎️  Asterisk ARI Caller - Исходящий звонок через Asterisk")
	fmt.Printf("📞 Звоним на номер: %s\n", *phoneNumber)
	fmt.Printf("🎵 Аудио файл: %s\n", *audioFile)

	// Запускаем WebSocket сервер если включен
	if *enableWS {
		go startWebSocketServer()
		fmt.Printf("🌐 WebSocket сервер запущен на ws://localhost%s/audio\n", WebSocketPort)
	}

	// Запускаем AudioSocket сервер для приема аудио от Asterisk
	go startAudioSocketServer()
	fmt.Printf("🎙️  AudioSocket сервер запущен на localhost%s\n", AudioSocketPort)

	// 1. Конвертируем аудио в PCM WAV
	fmt.Println("🎧 Конвертация аудио в PCM16 8kHz...")
	outputWav := fmt.Sprintf("%s/%s.wav", SoundsDir, OutputSound)
	if err := convertToUlaw(*audioFile, outputWav); err != nil {
		log.Fatalf("❌ Ошибка конвертации: %v", err)
	}

	// Проверяем что файл создался
	info, err := os.Stat(outputWav)
	if err != nil {
		log.Fatalf("❌ Файл не создан: %v", err)
	}
	fmt.Printf("✅ Аудио конвертировано: %s (%d байт)\n", outputWav, info.Size())

	// 2. Подключаемся к ARI
	fmt.Println("🔌 Подключение к Asterisk ARI...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cl, err := native.Connect(&native.Options{
		Application:  ARIApp,
		URL:          ARIHost,
		WebsocketURL: "ws://localhost:8088/ari/events",
		Username:     ARIUser,
		Password:     ARIPassword,
	})
	if err != nil {
		log.Fatalf("❌ Ошибка подключения к ARI: %v", err)
	}
	defer cl.Close()
	fmt.Println("✅ Подключено к ARI")

	// 3. Инициируем звонок
	fmt.Println("📤 Инициация звонка...")
	if err := makeCall(ctx, cl, *phoneNumber, OutputSound); err != nil {
		log.Fatalf("❌ Ошибка звонка: %v", err)
	}

	fmt.Println("✅ Звонок завершен!")
}

// convertToUlaw конвертирует аудио в PCM16 WAV 8kHz mono для Asterisk
func convertToUlaw(inputFile, outputFile string) error {
	// Создаем директорию если нет
	if err := os.MkdirAll(SoundsDir, 0755); err != nil {
		return err
	}

	// Конвертируем в обычный PCM16 WAV - Asterisk не принимает μ-law в WAV контейнере
	cmd := exec.Command("ffmpeg", "-y", "-i", inputFile,
		"-ac", "1", // mono
		"-ar", "8000", // 8kHz
		"-acodec", "pcm_s16le", // PCM 16-bit little-endian
		"-f", "wav",
		outputFile)

	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// makeCall совершает звонок и воспроизводит аудио
func makeCall(ctx context.Context, cl ari.Client, number, audioFile string) error {
	// Создаем локальный контекст который можно отменить
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Подписываемся на все события приложения
	sub := cl.Bus().Subscribe(nil, "StasisStart", "ChannelStateChange", "PlaybackFinished", "RecordingFinished", "ChannelDestroyed")
	defer sub.Cancel()

	var channelHandle *ari.ChannelHandle
	var snoopStarted bool
	var audioRecorder *AudioRecorder

	// Ждем событий в фоне
	go func() {
		for {
			select {
			case <-callCtx.Done():
				return
			case e := <-sub.Events():
				fmt.Printf("📨 Событие: %s\n", e.GetType())

				switch e.GetType() {
				case "StasisStart":
					// Получаем канал из события
					se := e.(*ari.StasisStart)

					// Игнорируем snoop канал (он создается для записи)
					if len(se.Channel.Name) > 0 && (se.Channel.Name[0:4] == "Snoo" || se.Channel.Name[0:5] == "snoop") {
						fmt.Printf("🔇 Snoop канал: %s\n", se.Channel.ID)
						continue
					}

					channelHandle = cl.Channel().Get(se.Key(ari.ChannelKey, se.Channel.ID))
					fmt.Printf("📞 Вызов начат, канал: %s\n", se.Channel.ID)

					// Проверяем состояние канала
					if se.Channel.State == "Up" {
						// Запускаем запись через AudioSocket СРАЗУ при ответе
						if !snoopStarted {
							snoopStarted = true
							audioRecorder = startAudioSocketRecording(cl, channelHandle)
						}

						fmt.Println("✅ Канал ответил, начинаем воспроизведение...")
						playAudio(channelHandle, audioFile)
					} else {
						fmt.Printf("⏳ Канал в состоянии: %s, ждем...\n", se.Channel.State)
					}

				case "ChannelStateChange":
					csc := e.(*ari.ChannelStateChange)
					fmt.Printf("📊 Состояние канала: %s\n", csc.Channel.State)

					if csc.Channel.State == "Up" && channelHandle != nil {
						// Запускаем запись через AudioSocket СРАЗУ при ответе
						if !snoopStarted {
							snoopStarted = true
							audioRecorder = startAudioSocketRecording(cl, channelHandle)
						}

						fmt.Println("✅ Вызов принят! Начинаем воспроизведение...")
						playAudio(channelHandle, audioFile)
					}

				case "PlaybackFinished":
					fmt.Println("✅ Воспроизведение завершено, продолжаем запись...")

				case "RecordingFinished":
					// Игнорируем - записи управляются вручную

				case "ChannelDestroyed":
					fmt.Println("📴 Канал завершен")
					// Сохраняем запись
					if audioRecorder != nil {
						audioRecorder.Stop()
					}
					cancel()
					return
				}
			}
		}
	}()

	// Инициируем исходящий звонок через Originate
	fmt.Println("📞 Набираем номер...")
	channelKey := ari.NewKey(ari.ChannelKey, "")
	h, err := cl.Channel().Originate(channelKey, ari.OriginateRequest{
		Endpoint: fmt.Sprintf("PJSIP/%s@zvonok", number),
		App:      ARIApp,
		Timeout:  30, // секунды
	})
	if err != nil {
		return fmt.Errorf("ошибка инициации звонка: %w", err)
	}

	fmt.Printf("✅ Звонок инициирован, канал: %s\n", h.ID())
	fmt.Println("⏳ Ожидание ответа и воспроизведения...")

	// Ждем завершения - НЕ закрываем соединение пока не придет ChannelDestroyed
	select {
	case <-callCtx.Done():
		fmt.Println("✅ Звонок завершен абонентом")
	case <-time.After(300 * time.Second):
		fmt.Println("⏱️  Таймаут звонка (5 минут)")
		if channelHandle != nil {
			channelHandle.Hangup()
		}
		cancel()
	}

	// Даем немного времени на cleanup
	time.Sleep(1 * time.Second)
	return nil
}

// playAudio воспроизводит аудио на канале
func playAudio(h *ari.ChannelHandle, audioFile string) {
	playbackID := fmt.Sprintf("playback-%d", time.Now().Unix())
	playbackURI := fmt.Sprintf("sound:custom/%s", audioFile)

	fmt.Printf("🎵 Воспроизведение: %s\n", playbackURI)

	_, err := h.Play(playbackID, playbackURI)
	if err != nil {
		log.Printf("❌ Ошибка воспроизведения: %v", err)
	}
}

// AudioRecorder записывает аудио батчами из AudioSocket
type AudioRecorder struct {
	samples       []int16
	mu            sync.Mutex
	done          chan struct{}
	recordingKey  string
	client        ari.Client
	sessionID     string
	chunkNum      int
	currentChunk  []int16
	lastChunkTime time.Time
}

// AudioSocketMessage типы сообщений
const (
	AudioSocketSilence = 0x00
	AudioSocketHangup  = 0x01
	AudioSocketAudio   = 0x10
	AudioSocketError   = 0x02
)

var (
	activeAudioSessions = make(map[string]*AudioRecorder)
	audioSessionsMu     sync.Mutex
)

// startAudioSocketRecording запускает запись через AudioSocket
func startAudioSocketRecording(cl ari.Client, mainChannel *ari.ChannelHandle) *AudioRecorder {
	timestamp := time.Now().Unix()
	sessionID := fmt.Sprintf("session-%d", timestamp)

	recorder := &AudioRecorder{
		samples:       make([]int16, 0, 8000*300), // ~5 минут
		done:          make(chan struct{}),
		client:        cl,
		sessionID:     sessionID,
		currentChunk:  make([]int16, 0, 8000), // 1 секунда при 8kHz
		lastChunkTime: time.Now(),
	}

	// Регистрируем сессию
	audioSessionsMu.Lock()
	activeAudioSessions[sessionID] = recorder
	audioSessionsMu.Unlock()

	fmt.Printf("🎙️  AudioSocket запись начата через Snoop (session: %s)\n", sessionID)

	// Создаем Snoop канал для получения аудио
	snoopID := fmt.Sprintf("snoop-%d", timestamp)
	snoopHandle, err := mainChannel.Snoop(snoopID, &ari.SnoopOptions{
		App:     ARIApp,
		Spy:     "in", // Только входящий = только голос собеседника
		Whisper: "none",
	})
	if err != nil {
		log.Printf("⚠️  Ошибка создания Snoop: %v", err)
		return recorder
	}

	fmt.Printf("🎧 Snoop канал создан для AudioSocket: %s\n", snoopID)

	// Запускаем AudioSocket на snoop канале
	go func() {
		// Небольшая задержка
		time.Sleep(200 * time.Millisecond)

		// Запускаем запись Snoop канала - она будет идти постоянно
		// И мы будем получать ее через копирование файлов
		// Но в будущем можно заменить на External Media для прямого стриминга
		_, err := snoopHandle.Record(fmt.Sprintf("%s-snoop", sessionID), &ari.RecordingOptions{
			Format: "wav",
			Beep:   false,
		})
		if err != nil {
			log.Printf("⚠️  Ошибка начала записи Snoop: %v", err)
		}
	}()

	// Запускаем таймер для сохранения батчей
	go func() {
		ticker := time.NewTicker(AudioSocketChunkDuration)
		defer ticker.Stop()

		for {
			select {
			case <-recorder.done:
				return
			case <-ticker.C:
				recorder.saveCurrentChunk()
			}
		}
	}()

	return recorder
}

// Старая функция Snoop - оставляем на случай если понадобится
func startSnoopRecording_DEPRECATED(cl ari.Client, mainChannel *ari.ChannelHandle) *AudioRecorder {
	recorder := &AudioRecorder{
		samples: make([]int16, 0, 8000*300), // ~5 минут
		done:    make(chan struct{}),
		client:  cl,
	}

	timestamp := time.Now().Unix()
	baseRecordingName := fmt.Sprintf("call-recording-%d", timestamp)

	fmt.Printf("🎙️  Начинаем запись с самого начала разговора (батчи по 1 сек)...\n")

	// Запускаем обычную запись которая будет сохранять все
	_, err := mainChannel.Record(baseRecordingName, &ari.RecordingOptions{
		Format: "wav",
		Beep:   false,
	})
	if err != nil {
		log.Printf("❌ Ошибка начала записи: %v", err)
		return recorder
	}

	recorder.recordingKey = baseRecordingName

	// Запускаем Snoop для получения аудио в реальном времени
	snoopID := fmt.Sprintf("snoop-%d", timestamp)
	snoopHandle, err := mainChannel.Snoop(snoopID, &ari.SnoopOptions{
		App:     ARIApp,
		Spy:     "both", // и входящий и исходящий
		Whisper: "none",
	})
	if err != nil {
		log.Printf("⚠️  Snoop не удалось создать: %v", err)
		return recorder
	}

	fmt.Printf("🎧 Snoop канал создан: %s\n", snoopID)

	// Запускаем запись snoop канала батчами по 1 секунде
	go func() {
		chunkNum := 0
		for {
			select {
			case <-recorder.done:
				// Останавливаем snoop
				snoopHandle.Hangup()
				return
			case <-time.After(1 * time.Second):
				chunkNum++
				chunkName := fmt.Sprintf("%s-chunk-%03d", baseRecordingName, chunkNum)

				// Запускаем запись чанка (1 сек)
				fmt.Printf("📦 Чанк #%d записывается...\n", chunkNum)

				_, err := snoopHandle.Record(chunkName, &ari.RecordingOptions{
					Format: "wav",
					Beep:   false,
				})
				if err != nil {
					log.Printf("⚠️  Ошибка записи чанка: %v", err)
					continue
				}

				// Останавливаем через 1 секунду
				go func(name string, num int) {
					time.Sleep(1 * time.Second)
					recHandle := cl.LiveRecording().Get(ari.NewKey(ari.LiveRecordingKey, name))
					if err := recHandle.Stop(); err == nil {
						// Копируем чанк
						if len(wsClients) > 0 {
							copyAndBroadcastChunk(name, num)
						}
					}
				}(chunkName, chunkNum)
			}
		}
	}()

	return recorder
}

// Stop останавливает запись и сохраняет
func (r *AudioRecorder) Stop() {
	close(r.done)

	fmt.Println("🛑 Останавливаем запись...")

	// Удаляем из активных сессий
	audioSessionsMu.Lock()
	delete(activeAudioSessions, r.sessionID)
	audioSessionsMu.Unlock()

	// Даем время Asterisk закончить запись
	time.Sleep(1 * time.Second)

	// Копируем Snoop запись из контейнера
	snoopRecordingName := fmt.Sprintf("%s-snoop", r.sessionID)
	if err := r.copySnoopRecording(snoopRecordingName); err != nil {
		log.Printf("❌ Ошибка копирования Snoop записи: %v", err)
	}
}

// copySnoopRecording копирует и разбивает Snoop запись на чанки
func (r *AudioRecorder) copySnoopRecording(recordingName string) error {
	if err := os.MkdirAll(RecordingsDir, 0755); err != nil {
		return err
	}

	// Путь к записи в контейнере
	containerPath := fmt.Sprintf("asterisk-caller:/var/spool/asterisk/recording/%s.wav", recordingName)
	fullRecordingPath := fmt.Sprintf("%s/output.wav", RecordingsDir)

	fmt.Printf("📥 Копируем полную запись из Snoop...\n")

	// Копируем полную запись
	cmd := exec.Command("docker", "cp", containerPath, fullRecordingPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("не удалось скопировать запись: %w", err)
	}

	// Проверяем размер
	info, err := os.Stat(fullRecordingPath)
	if err != nil {
		return err
	}

	fmt.Printf("✅ Полная запись сохранена: %s (%.1f KB)\n", fullRecordingPath, float64(info.Size())/1024.0)

	// Разбиваем на чанки по 1 секунде
	fmt.Println("📦 Разбиваем на чанки по 1 секунде...")
	if err := r.splitIntoChunks(fullRecordingPath); err != nil {
		log.Printf("⚠️  Ошибка разбиения на чанки: %v", err)
	}

	return nil
}

// splitIntoChunks разбивает WAV файл на чанки по 1 секунде
func (r *AudioRecorder) splitIntoChunks(wavPath string) error {
	// Открываем WAV файл
	f, err := os.Open(wavPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Читаем WAV заголовок (44 байта)
	header := make([]byte, 44)
	if _, err := io.ReadFull(f, header); err != nil {
		return err
	}

	// Читаем все аудио данные
	audioData, err := io.ReadAll(f)
	if err != nil {
		return err
	}

	// 8000 Hz, 16-bit, mono = 16000 байт/сек
	bytesPerSecond := 16000
	chunkSize := bytesPerSecond

	chunkNum := 0
	totalChunks := (len(audioData) + chunkSize - 1) / chunkSize

	for offset := 0; offset < len(audioData); offset += chunkSize {
		chunkNum++
		end := offset + chunkSize
		if end > len(audioData) {
			end = len(audioData)
		}

		chunkData := audioData[offset:end]
		chunkPath := fmt.Sprintf("%s/chunk-%03d.wav", RecordingsDir, chunkNum)

		// Создаем WAV файл для чанка
		if err := r.saveWAVChunk(chunkPath, header, chunkData); err != nil {
			log.Printf("⚠️  Ошибка сохранения чанка #%d: %v", chunkNum, err)
			continue
		}

		// Отправляем в WebSocket если есть клиенты
		if len(wsClients) > 0 {
			if data, err := os.ReadFile(chunkPath); err == nil {
				broadcastAudio(data)
			}
		}
	}

	fmt.Printf("✅ Создано %d чанков из %d\n", chunkNum, totalChunks)
	return nil
}

// saveWAVChunk сохраняет чанк аудио как WAV файл
func (r *AudioRecorder) saveWAVChunk(path string, header, audioData []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Обновляем размеры в заголовке
	dataSize := uint32(len(audioData))
	fileSize := 36 + dataSize

	// Копируем заголовок
	newHeader := make([]byte, 44)
	copy(newHeader, header)

	// Обновляем размеры (little-endian)
	binary.LittleEndian.PutUint32(newHeader[4:8], fileSize)
	binary.LittleEndian.PutUint32(newHeader[40:44], dataSize)

	// Записываем заголовок и данные
	if _, err := f.Write(newHeader); err != nil {
		return err
	}
	if _, err := f.Write(audioData); err != nil {
		return err
	}

	return nil
}

// saveCurrentChunk сохраняет текущий накопленный чанк
func (r *AudioRecorder) saveCurrentChunk() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.currentChunk) == 0 {
		return
	}

	r.chunkNum++

	// Добавляем к полной записи
	r.samples = append(r.samples, r.currentChunk...)

	// Сохраняем чанк в файл
	chunkPath := fmt.Sprintf("%s/chunk-%03d.wav", RecordingsDir, r.chunkNum)
	if err := saveAsWAV(chunkPath, r.currentChunk, 8000); err != nil {
		log.Printf("⚠️  Ошибка сохранения чанка #%d: %v", r.chunkNum, err)
	} else {
		fmt.Printf("📦 Чанк #%d сохранен (%d сэмплов)\n", r.chunkNum, len(r.currentChunk))

		// Отправляем в WebSocket если есть клиенты
		if len(wsClients) > 0 {
			if data, err := os.ReadFile(chunkPath); err == nil {
				broadcastAudio(data)
			}
		}
	}

	// Очищаем текущий чанк
	r.currentChunk = r.currentChunk[:0]
	r.lastChunkTime = time.Now()
}

// addAudioData добавляет аудио данные (вызывается из AudioSocket)
func (r *AudioRecorder) addAudioData(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Конвертируем байты в int16 samples (signed linear 16-bit)
	samples := make([]int16, len(data)/2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}

	r.currentChunk = append(r.currentChunk, samples...)
}

// saveFullRecording сохраняет всю запись в один файл
func (r *AudioRecorder) saveFullRecording(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(RecordingsDir, 0755); err != nil {
		return err
	}

	return saveAsWAV(path, r.samples, 8000)
}

// copyRecordingFromContainer копирует запись из контейнера
func copyRecordingFromContainer(recordingName string) error {
	if err := os.MkdirAll(RecordingsDir, 0755); err != nil {
		return err
	}

	containerPath := fmt.Sprintf("asterisk-caller:/var/spool/asterisk/recording/%s.wav", recordingName)
	localPath := fmt.Sprintf("%s/output.wav", RecordingsDir)

	fmt.Printf("📥 Копируем полную запись...\n")

	cmd := exec.Command("docker", "cp", containerPath, localPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("не удалось скопировать: %w", err)
	}

	fmt.Printf("✅ Полная запись сохранена: %s\n", localPath)
	return nil
}

// copyAndBroadcastChunk копирует чанк и транслирует через WebSocket
func copyAndBroadcastChunk(chunkName string, num int) {
	containerPath := fmt.Sprintf("asterisk-caller:/var/spool/asterisk/recording/%s.wav", chunkName)
	localPath := fmt.Sprintf("%s/chunk-%03d.wav", RecordingsDir, num)

	cmd := exec.Command("docker", "cp", containerPath, localPath)
	if err := cmd.Run(); err != nil {
		return
	}

	// Читаем чанк и отправляем в WebSocket
	data, err := os.ReadFile(localPath)
	if err == nil && len(wsClients) > 0 {
		broadcastAudio(data)
		fmt.Printf("📡 Чанк #%d отправлен в WebSocket (%d байт)\n", num, len(data))
	}
}

// copyAllChunks копирует все чанки после завершения звонка
func copyAllChunks(baseRecordingName string) {
	if err := os.MkdirAll(RecordingsDir, 0755); err != nil {
		log.Printf("❌ Ошибка создания директории: %v", err)
		return
	}

	// Находим все чанки в контейнере
	cmd := exec.Command("docker", "exec", "asterisk-caller", "sh", "-c",
		fmt.Sprintf("ls /var/spool/asterisk/recording/%s-chunk-*.wav 2>/dev/null", baseRecordingName))
	output, err := cmd.Output()
	if err != nil {
		log.Printf("⚠️  Чанки не найдены")
		return
	}

	// Копируем каждый чанк
	chunkCount := 0
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		chunkCount++
		// Извлекаем имя файла
		filename := line[strings.LastIndex(line, "/")+1:]
		containerPath := fmt.Sprintf("asterisk-caller:%s", line)
		localPath := fmt.Sprintf("%s/%s", RecordingsDir, filename)

		cmd := exec.Command("docker", "cp", containerPath, localPath)
		if err := cmd.Run(); err != nil {
			log.Printf("⚠️  Ошибка копирования чанка %s", filename)
		}
	}

	if chunkCount > 0 {
		fmt.Printf("✅ Скопировано %d чанков в %s/\n", chunkCount, RecordingsDir)
	}
}

// startWebSocketServer запускает WebSocket сервер для трансляции аудио
func startWebSocketServer() {
	// Обработчик широковещательных сообщений
	go func() {
		for {
			msg := <-wsBroadcast
			wsClientsMu.Lock()
			for client := range wsClients {
				err := client.WriteMessage(websocket.BinaryMessage, msg)
				if err != nil {
					log.Printf("WebSocket ошибка: %v", err)
					client.Close()
					delete(wsClients, client)
				}
			}
			wsClientsMu.Unlock()
		}
	}()

	// HTTP сервер
	http.HandleFunc("/audio", handleWebSocket)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("WebSocket Audio Stream Server. Connect to /audio"))
	})

	log.Fatal(http.ListenAndServe(WebSocketPort, nil))
}

// handleWebSocket обрабатывает WebSocket подключения
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	wsClientsMu.Lock()
	wsClients[conn] = true
	wsClientsMu.Unlock()

	fmt.Printf("🌐 Новое WebSocket подключение (всего: %d)\n", len(wsClients))

	// Держим соединение открытым
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			wsClientsMu.Lock()
			delete(wsClients, conn)
			wsClientsMu.Unlock()
			fmt.Printf("🌐 WebSocket отключен (осталось: %d)\n", len(wsClients))
			break
		}
	}
}

// broadcastAudio отправляет аудио-чанк всем WebSocket клиентам
func broadcastAudio(data []byte) {
	select {
	case wsBroadcast <- data:
	default:
		// Буфер полон, пропускаем
	}
}

// startAudioSocketServer запускает TCP сервер для приема AudioSocket от Asterisk
func startAudioSocketServer() {
	listener, err := net.Listen("tcp", AudioSocketPort)
	if err != nil {
		log.Fatalf("❌ Не удалось запустить AudioSocket сервер: %v", err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("⚠️  Ошибка accept: %v", err)
			continue
		}

		go handleAudioSocketConnection(conn)
	}
}

// handleAudioSocketConnection обрабатывает одно AudioSocket соединение
func handleAudioSocketConnection(conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	fmt.Printf("🔌 AudioSocket подключение от %s\n", remoteAddr)

	// Читаем UUID из первого сообщения (если используется)
	// Для простоты используем первую активную сессию
	var recorder *AudioRecorder

	audioSessionsMu.Lock()
	for _, r := range activeAudioSessions {
		recorder = r
		break
	}
	audioSessionsMu.Unlock()

	if recorder == nil {
		log.Printf("⚠️  Нет активной сессии записи для AudioSocket")
		return
	}

	fmt.Printf("📡 AudioSocket сессия связана с %s\n", recorder.sessionID)

	// Читаем AudioSocket протокол
	for {
		// Читаем заголовок (3 байта)
		header := make([]byte, 3)
		n, err := io.ReadFull(conn, header)
		if err != nil {
			if err != io.EOF {
				log.Printf("⚠️  Ошибка чтения заголовка: %v", err)
			}
			break
		}
		if n != 3 {
			log.Printf("⚠️  Неполный заголовок: %d байт", n)
			break
		}

		msgType := header[0]
		length := binary.BigEndian.Uint16(header[1:3])

		// Читаем данные
		data := make([]byte, length)
		if length > 0 {
			n, err := io.ReadFull(conn, data)
			if err != nil {
				log.Printf("⚠️  Ошибка чтения данных: %v", err)
				break
			}
			if uint16(n) != length {
				log.Printf("⚠️  Неполные данные: %d из %d байт", n, length)
				break
			}
		}

		// Обрабатываем по типу сообщения
		switch msgType {
		case AudioSocketSilence:
			// Тишина - пропускаем или добавляем нули
			continue

		case AudioSocketAudio:
			// Аудио данные - добавляем к рекордеру
			recorder.addAudioData(data)

		case AudioSocketHangup:
			fmt.Println("📞 AudioSocket: канал завершен")
			return

		case AudioSocketError:
			log.Printf("❌ AudioSocket error: %s", string(data))
			return

		default:
			log.Printf("⚠️  Неизвестный тип сообщения AudioSocket: 0x%02x", msgType)
		}
	}

	fmt.Printf("🔌 AudioSocket отключен от %s\n", remoteAddr)
}

// saveAsWAV сохраняет PCM16 сэмплы как WAV файл
func saveAsWAV(filename string, samples []int16, sampleRate int) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// WAV заголовок
	numSamples := len(samples)
	dataSize := numSamples * 2 // 16 бит = 2 байта

	// RIFF заголовок
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataSize))
	f.Write([]byte("WAVE"))

	// fmt chunk
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))           // размер fmt chunk
	binary.Write(f, binary.LittleEndian, uint16(1))            // аудио формат PCM
	binary.Write(f, binary.LittleEndian, uint16(1))            // моно
	binary.Write(f, binary.LittleEndian, uint32(sampleRate))   // sample rate
	binary.Write(f, binary.LittleEndian, uint32(sampleRate*2)) // byte rate
	binary.Write(f, binary.LittleEndian, uint16(2))            // block align
	binary.Write(f, binary.LittleEndian, uint16(16))           // bits per sample

	// data chunk
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, uint32(dataSize))

	// Записываем сэмплы
	return binary.Write(f, binary.LittleEndian, samples)
}
