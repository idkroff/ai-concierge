package asterisk

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/staskobzar/goami2"
)

const (
	// AudioSocket message types
	AudioSocketSilence = 0x00
	AudioSocketHangup  = 0x01
	AudioSocketAudio   = 0x10
	AudioSocketError   = 0x02
)

// Config содержит конфигурацию для Asterisk клиента
type Config struct {
	AMIHost         string
	AMIPort         string
	AMIUser         string
	AMIPassword     string
	AudioSocketPort string
	PJSIPEndpoint  string // Имя PJSIP endpoint/trunk для исходящих (например zvonok)
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() Config {
	amiHost := os.Getenv("AMI_HOST")
	if amiHost == "" {
		amiHost = "127.0.0.1"
	}

	amiPort := os.Getenv("AMI_PORT")
	if amiPort == "" {
		amiPort = "5038"
	}

	amiUser := os.Getenv("AMI_USER")
	if amiUser == "" {
		amiUser = "goami"
	}

	amiPassword := os.Getenv("AMI_PASSWORD")
	if amiPassword == "" {
		amiPassword = "goamisecret123"
	}

	audioSocketPort := os.Getenv("AUDIO_SOCKET_PORT")
	if audioSocketPort == "" {
		audioSocketPort = ":9092"
	}

	pjsipEndpoint := os.Getenv("PJSIP_ENDPOINT")
	if pjsipEndpoint == "" {
		pjsipEndpoint = "zvonok"
	}

	return Config{
		AMIHost:         amiHost,
		AMIPort:         amiPort,
		AMIUser:         amiUser,
		AMIPassword:     amiPassword,
		AudioSocketPort: audioSocketPort,
		PJSIPEndpoint:   pjsipEndpoint,
	}
}

// Client представляет клиент для работы с Asterisk AMI
type Client struct {
	config    Config
	amiClient *goami2.Client
	amiConn   net.Conn

	// AudioSocket
	audioListener     net.Listener
	audioSessions     map[string]*AudioSession // sessionID -> AudioSession
	channelToSession  map[string]*AudioSession // channelID -> AudioSession
	audioSessionsMu   sync.Mutex
	pendingSessions   []*AudioSession // Сессии ожидающие установки ChannelID
	pendingSessionsMu sync.Mutex

	// Управление
	ctx      context.Context
	cancel   context.CancelFunc
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// AudioSession представляет сессию аудио-стрима
type AudioSession struct {
	ID               string
	ChannelID        string      // Asterisk Channel Uniqueid
	AudioInput       chan []byte // Аудио от абонента (8kHz PCM16)
	AudioOutput      chan []byte // Аудио для воспроизведения абоненту (8kHz PCM16)
	Done             chan struct{}
	AudioSocketReady chan struct{} // Сигнал что AudioSocket подключен
	AllAudioSent     chan struct{} // Сигнал что все аудио отправлено в Asterisk
	AudioOutputDone  chan struct{} // Сигнал от call_service: Yandex закончил генерацию аудио
	doneOnce             sync.Once // Защита от повторного закрытия
	readyOnce            sync.Once // Защита от повторного закрытия ready
	audioSentOnce        sync.Once // Защита от повторного закрытия audioSent
	audioOutputDoneOnce  sync.Once // Защита от повторного закрытия audioOutputDone
}

// SignalAudioOutputDone сигнализирует Producer-у что новых аудио-данных от Yandex не будет.
// Producer сольёт остатки в буфер и завершится, что позволит AllAudioSent сработать штатно.
func (s *AudioSession) SignalAudioOutputDone() {
	s.audioOutputDoneOnce.Do(func() {
		close(s.AudioOutputDone)
	})
}

// NewClient создает новый Asterisk клиент
func NewClient(config Config) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		config:           config,
		audioSessions:    make(map[string]*AudioSession),
		channelToSession: make(map[string]*AudioSession),
		pendingSessions:  make([]*AudioSession, 0),
		stopChan:         make(chan struct{}),
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Connect подключается к Asterisk AMI
func (c *Client) Connect(ctx context.Context) error {
	amiAddress := fmt.Sprintf("%s:%s", c.config.AMIHost, c.config.AMIPort)
	log.Printf("🔌 Подключение к AMI: %s (user: %s)\n", amiAddress, c.config.AMIUser)

	// Подключаемся к AMI
	conn, err := net.Dial("tcp", amiAddress)
	if err != nil {
		return fmt.Errorf("ошибка подключения к AMI: %w", err)
	}

	// Создаем клиент
	amiClient, err := goami2.NewClient(conn, c.config.AMIUser, c.config.AMIPassword)
	if err != nil {
		conn.Close()
		return fmt.Errorf("ошибка авторизации AMI: %w", err)
	}

	c.amiClient = amiClient
	c.amiConn = conn
	log.Println("✅ Подключено к Asterisk AMI")

	// Запускаем AudioSocket сервер
	if err := c.startAudioSocketServer(); err != nil {
		return fmt.Errorf("ошибка запуска AudioSocket: %w", err)
	}

	// Запускаем обработчик AMI событий
	go c.handleAMIEvents()

	return nil
}

// MakeCall совершает исходящий звонок через AMI Originate
func (c *Client) MakeCall(ctx context.Context, phoneNumber string) (*AudioSession, error) {
	sessionID := uuid.New().String()
	session := &AudioSession{
		ID:               sessionID,
		AudioInput:       make(chan []byte, 1000),
		AudioOutput:      make(chan []byte, 1000),
		Done:             make(chan struct{}),
		AudioSocketReady: make(chan struct{}),
		AllAudioSent:     make(chan struct{}),
		AudioOutputDone:  make(chan struct{}),
	}

	// Регистрируем сессию по sessionID
	c.audioSessionsMu.Lock()
	c.audioSessions[sessionID] = session
	c.audioSessionsMu.Unlock()

	// Добавляем в pending до получения ChannelID
	c.pendingSessionsMu.Lock()
	c.pendingSessions = append(c.pendingSessions, session)
	c.pendingSessionsMu.Unlock()

	// Формируем AMI Originate запрос
	msg := goami2.NewAction("Originate")
	msg.AddActionID()
	msg.AddField("Channel", fmt.Sprintf("PJSIP/%s@%s", phoneNumber, c.config.PJSIPEndpoint))
	msg.AddField("Context", "audiosocket")
	msg.AddField("Exten", "s")
	msg.AddField("Priority", "1")
	msg.AddField("Async", "yes")
	msg.AddField("Timeout", "30000")
	msg.AddField("Variable", fmt.Sprintf("SESSION_ID=%s", sessionID))

	log.Printf("📤 Отправка Originate на %s\n", phoneNumber)
	c.amiClient.Send(msg.Byte())

	log.Printf("📞 Звонок инициирован через AMI на %s (session: %s)\n", phoneNumber, sessionID)

	return session, nil
}

// handleAMIEvents обрабатывает события от AMI
func (c *Client) handleAMIEvents() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.stopChan:
			return
		case msg := <-c.amiClient.AllMessages():
			eventType := msg.Field("Event")

			switch eventType {
			case "Newchannel":
				channelID := msg.Field("Uniqueid")
				log.Printf("🔍 Newchannel: Uniqueid=%s", channelID)

				// Связываем с первой pending сессией
				c.pendingSessionsMu.Lock()
				if len(c.pendingSessions) > 0 {
					session := c.pendingSessions[0]
					if session.ChannelID == "" {
						session.ChannelID = channelID
						// Регистрируем в channelToSession
						c.audioSessionsMu.Lock()
						c.channelToSession[channelID] = session
						c.audioSessionsMu.Unlock()
						log.Printf("✅ ChannelID %s установлен для сессии %s из Newchannel", channelID, session.ID)
					}
				}
				c.pendingSessionsMu.Unlock()

			case "Hangup":
				channelID := msg.Field("Uniqueid")
				c.handleHangup(channelID)

			case "OriginateResponse":
				response := msg.Field("Response")
				uniqueid := msg.Field("Uniqueid")
				reason := msg.Field("Reason")
				amiMsg := msg.Field("Message")
				channel := msg.Field("Channel")

				log.Printf("🔍 OriginateResponse: Response=%s, Uniqueid=%s, Reason=%s, Channel=%s", response, uniqueid, reason, channel)
				if amiMsg != "" {
					log.Printf("🔍 OriginateResponse Message: %s", amiMsg)
				}

				if response == "Failure" {
					log.Printf("⚠️  Originate failed: Reason=%s, Message=%s, Channel=%s", reason, amiMsg, channel)

					// Удаляем из pending
					c.pendingSessionsMu.Lock()
					if len(c.pendingSessions) > 0 {
						c.pendingSessions = c.pendingSessions[1:]
					}
					c.pendingSessionsMu.Unlock()
				} else if response == "Success" {
					log.Printf("✅ Originate успешен, Uniqueid=%s\n", uniqueid)

					// Устанавливаем ChannelID если еще не установлен
					c.pendingSessionsMu.Lock()
					if len(c.pendingSessions) > 0 {
						session := c.pendingSessions[0]
						if session.ChannelID == "" && uniqueid != "" {
							session.ChannelID = uniqueid
							// Регистрируем в channelToSession
							c.audioSessionsMu.Lock()
							c.channelToSession[uniqueid] = session
							c.audioSessionsMu.Unlock()
							log.Printf("✅ ChannelID %s установлен для сессии %s из OriginateResponse", uniqueid, session.ID)
						}
						// Удаляем из pending после успешного Originate
						c.pendingSessions = c.pendingSessions[1:]
					}
					c.pendingSessionsMu.Unlock()
				}
			}

		case err := <-c.amiClient.Err():
			if errors.Is(err, goami2.ErrEOF) {
				log.Printf("❌ Соединение AMI разорвано: %s\n", err)
				return
			}
			log.Printf("⚠️  Ошибка AMI: %s\n", err)
		}
	}
}

// handleHangup обрабатывает завершение звонка
func (c *Client) handleHangup(channelID string) {
	log.Printf("📴 Hangup для канала: %s\n", channelID)

	c.audioSessionsMu.Lock()
	defer c.audioSessionsMu.Unlock()

	// Находим сессию по channelID через быструю мапу
	session, exists := c.channelToSession[channelID]
	if exists && session != nil {
		log.Printf("📴 Завершение сессии: %s\n", session.ID)
		session.doneOnce.Do(func() {
			close(session.Done)
		})
		delete(c.audioSessions, session.ID)
		delete(c.channelToSession, channelID)
	} else {
		log.Printf("⚠️  Сессия не найдена для канала: %s\n", channelID)
	}
}

// startAudioSocketServer запускает TCP сервер для AudioSocket
func (c *Client) startAudioSocketServer() error {
	listener, err := net.Listen("tcp", c.config.AudioSocketPort)
	if err != nil {
		return fmt.Errorf("не удалось запустить AudioSocket сервер: %w", err)
	}

	c.audioListener = listener
	fmt.Printf("🎙️  AudioSocket сервер запущен на localhost%s\n", c.config.AudioSocketPort)
	fmt.Printf("💡 Ожидаем подключения от Asterisk на порту %s...\n", c.config.AudioSocketPort)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case <-c.stopChan:
				return
			default:
			}

			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-c.stopChan:
					return
				default:
					log.Printf("⚠️  Ошибка accept: %v", err)
					continue
				}
			}

			log.Printf("🔌 AudioSocket: входящее TCP-соединение от %s\n", conn.RemoteAddr())
			go c.handleAudioSocketConnection(conn)
		}
	}()

	return nil
}

// handleAudioSocketConnection обрабатывает одно AudioSocket соединение
func (c *Client) handleAudioSocketConnection(conn net.Conn) {
	defer conn.Close()

	// Отключаем алгоритм Nagle для немедленной отправки пакетов
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		log.Printf("✅ TCP NoDelay включен для немедленной отправки пакетов")
	}

	remoteAddr := conn.RemoteAddr().String()

	// Читаем первый пакет чтобы получить UUID из диалплана
	// UUID передается в первом AudioSocket пакете
	header := make([]byte, 3)
	_, err := io.ReadFull(conn, header)
	if err != nil {
		log.Printf("⚠️  AudioSocket %s: ошибка чтения первого пакета: %v\n", remoteAddr, err)
		return
	}

	length := binary.BigEndian.Uint16(header[1:3])

	uuidBytes := make([]byte, length)
	if length > 0 {
		_, err = io.ReadFull(conn, uuidBytes)
		if err != nil {
			log.Printf("⚠️  Ошибка чтения UUID: %v\n", err)
			return
		}
	}

	var sessionID string
	if length == 16 {
		// Бинарный формат UUID (16 байт)
		parsedUUID, err := uuid.FromBytes(uuidBytes)
		if err != nil {
			log.Printf("⚠️  Ошибка парсинга бинарного UUID: %v\n", err)
		} else {
			sessionID = parsedUUID.String()
			log.Printf("📋 AudioSocket UUID (бинарный): %s\n", sessionID)
		}
	} else {
		log.Printf("⚠️  Неожиданная длина UUID: %d байт, содержимое: %q\n", length, sessionID)
		return
	}

	c.audioSessionsMu.Lock()
	session, exists := c.audioSessions[sessionID]
	c.audioSessionsMu.Unlock()

	if !exists || session == nil {
		log.Printf("❌ Сессия не найдена для SESSION_ID: %s\n", sessionID)
		log.Printf("📋 Доступные сессии:")
		c.audioSessionsMu.Lock()
		for sid := range c.audioSessions {
			log.Printf("   - %s\n", sid)
		}
		c.audioSessionsMu.Unlock()
		return
	}

	fmt.Printf("✅ AudioSocket подключен к сессии %s\n", session.ID)

	session.readyOnce.Do(func() {
		close(session.AudioSocketReady)
		fmt.Println("✅ AudioSocket готов к передаче данных")
	})

	var packetsReceived int
	var bytesReceived int64
	var packetsSent int
	var bytesSent int64

	// Goroutine 1: Чтение аудио от Asterisk (голос абонента)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer log.Printf("Read goroutine завершена")

		for {
			select {
			case <-session.Done:
				return
			default:
			}

			// Читаем заголовок (3 байта)
			header := make([]byte, 3)
			n, err := io.ReadFull(conn, header)
			if err != nil {
				if err == io.EOF {
					log.Printf("Read: AudioSocket закрыт Asterisk-ом (EOF)")
				} else {
					log.Printf("⚠️  Read: ошибка чтения заголовка: %v", err)
				}
				return
			}
			if n != 3 {
				log.Printf("🛑 Read: прочитано %d байт вместо 3, завершаемся", n)
				return
			}

			msgType := header[0]
			length := binary.BigEndian.Uint16(header[1:3])

			// Читаем данные
			data := make([]byte, length)
			if length > 0 {
				n, err := io.ReadFull(conn, data)
				if err != nil || uint16(n) != length {
					return
				}
			}

			// Обрабатываем по типу сообщения
			switch msgType {
			case AudioSocketSilence:
				// Тишина
				if packetsReceived%100 == 0 && packetsReceived > 0 {
					fmt.Printf("🔇 AudioSocket: получено %d пакетов тишины\n", packetsReceived)
				}
				packetsReceived++

			case AudioSocketAudio:
				packetsReceived++
				bytesReceived += int64(length)

				// Логируем первые несколько пакетов
				if packetsReceived <= 5 || packetsReceived%50 == 0 {
					fmt.Printf("🎤 AudioSocket пакет #%d: %d байт аудио (всего: %d байт)\n",
						packetsReceived, length, bytesReceived)
				}

				// Отправляем аудио в канал (это голос абонента)
				select {
				case session.AudioInput <- data:
					if packetsReceived <= 3 {
						fmt.Printf("✅ Аудио отправлено в session.AudioInput\n")
					}
				default:
					// Логируем переполнение буфера только раз в 100 потерянных пакетов
					if packetsReceived%100 == 0 {
						log.Printf("⚠️  AudioInput буфер полон! Пакеты теряются (получено: %d)\n", packetsReceived)
					}
				}

			case AudioSocketHangup:
				fmt.Printf("📞 AudioSocket: канал завершен (получено %d пакетов, %d байт)\n",
					packetsReceived, bytesReceived)
				return

			case AudioSocketError:
				log.Printf("❌ AudioSocket error: %s", string(data))
				return

			default:
				log.Printf("⚠️  Неизвестный тип сообщения AudioSocket: 0x%02x (длина: %d)\n", msgType, length)
			}
		}
	}()

	// Goroutine 2 & 3: Отправка аудио в Asterisk с буферизацией и таймером
	// Архитектура: Producer-Consumer pattern
	// Producer (горутина 2): разбивает чанки от Yandex на пакеты 320 байт → буфер
	// Consumer (горутина 3): читает из буфера по таймеру 20ms → AudioSocket

	packetBuffer := make(chan []byte, 200) // Буфер на ~4 секунды (200 пакетов * 20ms)
	writeDone := make(chan struct{})
	producerDone := make(chan struct{})

	// Goroutine 2 (Producer): разбиваем чанки на пакеты по 320 байт
	go func() {
		defer close(producerDone)
		defer log.Printf("✅ Producer завершен, сигнализируем Consumer")
		const chunkSize = 320

		// chunkAndBuffer разбивает audioData на пакеты по 320 байт и кладёт в буфер.
		// Возвращает false если нужно завершиться (session.Done или readDone).
		chunkAndBuffer := func(audioData []byte) bool {
			for offset := 0; offset < len(audioData); offset += chunkSize {
				end := offset + chunkSize
				if end > len(audioData) {
					end = len(audioData)
				}
				chunkLen := end - offset
				chunk := make([]byte, chunkSize)
				copy(chunk, audioData[offset:end])
				if chunkLen < chunkSize {
					log.Printf("⚠️  Producer: последний фрагмент %d байт, дополнен до 320 нулями", chunkLen)
				}
				select {
				case packetBuffer <- chunk:
				case <-session.Done:
					return false
				case <-readDone:
					return false
				}
			}
			return true
		}

		audioOutputDoneCh := session.AudioOutputDone

		for {
			select {
			case <-session.Done:
				return
			case <-readDone:
				return
			case <-audioOutputDoneCh:
				// Yandex больше не пришлёт аудио. Сливаем то, что ещё в пути.
				// 2с — запас для in-flight chunks от горутины Yandex-аудио.
				// Drain завершится раньше как только канал опустеет (default в select).
				audioOutputDoneCh = nil
				log.Printf("✅ Producer: сигнал AudioOutputDone, сливаем остатки...")
				// Читаем из канала пока есть данные; выходим когда канал пуст 50мс подряд
			// или общий таймаут 2с истёк (страховка).
			drainTimeout := time.NewTimer(2 * time.Second)
			idleTimer := time.NewTimer(50 * time.Millisecond)
			defer drainTimeout.Stop()
			defer idleTimer.Stop()
			drain:
				for {
					select {
					case <-session.Done:
						return
					case <-readDone:
						return
					case <-drainTimeout.C:
						log.Printf("⚠️  Producer: таймаут слива (2с)")
						break drain
					case <-idleTimer.C:
						// 50мс тишины — канал опустел
						break drain
					case audioData, ok := <-session.AudioOutput:
						if !ok {
							break drain
						}
						if len(audioData) > 0 {
							if !chunkAndBuffer(audioData) {
								return
							}
						}
						// Сбрасываем idle-таймер: данные ещё идут
						if !idleTimer.Stop() {
							select {
							case <-idleTimer.C:
							default:
							}
						}
						idleTimer.Reset(50 * time.Millisecond)
					}
				}
				log.Printf("✅ Producer: слив завершён, выходим")
				return
			case audioData, ok := <-session.AudioOutput:
				if !ok {
					log.Printf("✅ Producer: канал AudioOutput закрыт. Завершаем Producer")
					return
				}
				if len(audioData) == 0 {
					continue
				}
				log.Printf("🎵 Producer: получен чанк от Yandex %d байт, разбиваем на пакеты по 320", len(audioData))
				if !chunkAndBuffer(audioData) {
					return
				}
			}
		}
	}()

	// Goroutine 3 (Consumer): отправляем пакеты по таймеру 20ms
	go func() {
		defer close(writeDone)
		defer log.Printf("Write goroutine завершена (отправлено %d пакетов)", packetsSent)

		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		var producerFinished bool
		var emptyTicksAfterProducer int
		producerDoneCh := producerDone

		for {
			select {
			case <-session.Done:
				return
			case <-readDone:
				return
			case <-producerDoneCh:
				log.Printf("✅ Consumer: Producer завершен, будем отправлять остатки из буфера")
				producerFinished = true
				producerDoneCh = nil // закрытый канал срабатывал бы бесконечно
			case <-ticker.C:
				var chunk []byte
				var msgType byte

				select {
				case audioData, ok := <-packetBuffer:
					if !ok {
						return
					}
					chunk = audioData
					msgType = AudioSocketAudio
					emptyTicksAfterProducer = 0
				default:
					if producerFinished {
						emptyTicksAfterProducer++
						if emptyTicksAfterProducer >= 10 {
							session.audioSentOnce.Do(func() {
								log.Printf("✅ Consumer: все аудио отправлено в Asterisk")
								close(session.AllAudioSent)
							})
						}
					}
					continue
				}

				// Формируем AudioSocket пакет: 3 байта header + data
				length := uint16(len(chunk))

				// КРИТИЧЕСКИ ВАЖНО: отправляем header и data ОДНИМ вызовом Write()
				// чтобы избежать фрагментации пакета в TCP
				packet := make([]byte, 3+len(chunk))
				packet[0] = msgType
				packet[1] = byte(length >> 8)
				packet[2] = byte(length & 0xFF)
				copy(packet[3:], chunk)

				// Отправляем весь пакет атомарно
				n, err := conn.Write(packet)
				if err != nil {
					select {
					case <-session.Done:
					case <-readDone:
					default:
						log.Printf("⚠️  Ошибка отправки пакета: %v", err)
					}
					return
				}
				if n != len(packet) {
					log.Printf("⚠️⚠️  ОШИБКА: отправлено %d байт вместо %d!", n, len(packet))
					return
				}

				packetsSent++

				// Обновляем статистику только для аудио пакетов
				if msgType == AudioSocketAudio {
					bytesSent += int64(length)
				}

				// Проверяем корректность размера
				if msgType == AudioSocketAudio && length != 320 {
					log.Printf("⚠️⚠️⚠️  ОШИБКА: Аудио пакет неправильного размера! %d байт вместо 320", length)
				}
				if msgType == AudioSocketSilence && length != 320 {
					log.Printf("⚠️⚠️⚠️  ОШИБКА: Пакет тишины неправильного размера! %d байт вместо 320", length)
				}

				// Логируем первые пакеты и каждый 50-й
				if packetsSent <= 5 || packetsSent%50 == 0 {
					if msgType == AudioSocketAudio {
						fmt.Printf("🔊 AudioSocket отправлено аудио #%d: %d байт (всего: %d байт)\n",
							packetsSent, length, bytesSent)
					} else {
						fmt.Printf("🔇 AudioSocket отправлено тишину #%d: %d байт\n", packetsSent, length)
					}
				}
			}
		}
	}()

	// Ждем завершения обеих горутин
	<-readDone
	<-writeDone

	fmt.Printf("🔌 AudioSocket отключен от %s (получено %d/%d байт, отправлено %d/%d байт)\n",
		remoteAddr, packetsReceived, bytesReceived, packetsSent, bytesSent)
}

// Close закрывает клиент
func (c *Client) Close() error {
	close(c.stopChan)
	c.cancel()

	if c.audioListener != nil {
		c.audioListener.Close()
	}

	if c.amiClient != nil {
		c.amiClient.Close()
	}

	if c.amiConn != nil {
		c.amiConn.Close()
	}

	c.wg.Wait()
	return nil
}

// Hangup завершает звонок
func (c *Client) Hangup(session *AudioSession) error {
	if session.ChannelID == "" {
		log.Printf("❌ Hangup: ChannelID не установлен для сессии %s", session.ID)
		return fmt.Errorf("channel ID not set")
	}

	log.Printf("📞 Hangup: отправляем команду завершения для канала %s", session.ChannelID)
	msg := goami2.NewAction("Hangup")
	msg.AddField("Channel", session.ChannelID)
	c.amiClient.Send(msg.Byte())
	log.Printf("✅ Hangup: команда отправлена для канала %s", session.ChannelID)

	// Закрываем session.Done чтобы остановить все горутины
	session.doneOnce.Do(func() {
		close(session.Done)
		log.Printf("✅ Hangup: session.Done закрыт, горутины остановятся")
	})

	return nil
}
