package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"yandex_realtime/pkg/audio"
	"yandex_realtime/pkg/yandex"

	"github.com/joho/godotenv"
)

func main() {
	// Загружаем переменные из .env файла
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  Файл .env не найден, используем переменные окружения")
	}

	// Получаем параметры из переменных окружения (загруженных из .env или системных)
	apiKey := os.Getenv("YANDEX_API_KEY")
	folder := os.Getenv("YANDEX_FOLDER_ID")

	if apiKey == "" || folder == "" {
		log.Fatal("❌ Необходимо установить переменные YANDEX_API_KEY и YANDEX_FOLDER_ID в файле .env или в переменных окружения")
	}

	// Создаем клиент
	instructions := `Ты - дружелюбный голосовой помощник. 
Отвечай кратко и по существу. 
Говори естественным разговорным языком.`

	client := yandex.NewClient(apiKey, folder, instructions)

	// Подключаемся
	fmt.Println("🔌 Подключаемся к Yandex Realtime API...")
	if err := client.Connect(); err != nil {
		log.Fatalf("❌ Ошибка подключения: %v", err)
	}
	defer client.Close()

	fmt.Println("✅ Подключено успешно!")

	// Ждем, пока сессия будет готова
	fmt.Println("⏳ Ждем готовности сессии...")
	for !client.IsSessionReady() {
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("✅ Сессия готова!")

	// Запускаем горутину для обработки аудио ответов
	go handleAudioOutput(client)

	// Запускаем горутину для обработки текстовых ответов
	go handleTextOutput(client)

	// Запускаем горутину для обработки событий VAD и генерации ответов (как в оригинале)
	speechDetected := false
	go func() {
		for event := range client.Events() {
			switch event.Type {
			case "session.updated":
				fmt.Println("📡 [DEBUG] Сессия обновлена")

			case "input_audio_buffer.speech_started":
				fmt.Println("\n🎤 Речь обнаружена")
				speechDetected = true

			case "input_audio_buffer.speech_stopped":
				fmt.Println("🔇 Речь остановлена")

			case "input_audio_buffer.committed":
				if speechDetected {
					fmt.Println("✅ Аудио буфер зафиксирован, генерируем ответ...")
					// Запрашиваем генерацию ответа (как в оригинале)
					if err := client.TriggerResponse("Ответь на услышанную речь."); err != nil {
						log.Printf("⚠️  Ошибка запроса ответа: %v", err)
					}
					speechDetected = false
				}

			case "response.created":
				fmt.Println("🤖 Генерация ответа начата")

			case "response.done":
				fmt.Println("✅ Ответ завершен")

			case "error":
				fmt.Printf("❌ Ошибка от API: %+v\n", event)

			default:
				fmt.Printf("📡 [DEBUG] Событие: %s\n", event.Type)
			}
		}
	}()

	// Путь к аудио файлу
	audioPath := filepath.Join("..", "..", "concierge", "input2.mp3")

	// Проверяем существование файла
	if _, err := os.Stat(audioPath); err != nil {
		log.Printf("⚠️  Файл %s не найден, используем текстовый запрос\n", audioPath)
		// Отправляем текстовый запрос как fallback
		fmt.Println("\n📝 Отправляем тестовый запрос...")
		if err := client.TriggerResponse("Привет! Расскажи, кто ты и что умеешь?"); err != nil {
			log.Printf("❌ Ошибка отправки запроса: %v", err)
		}
		time.Sleep(10 * time.Second)
	} else {
		// Конвертируем и отправляем аудио
		fmt.Printf("\n🎵 Конвертируем аудио файл: %s\n", audioPath)
		audioData, err := audio.ConvertAndRead(audioPath)
		if err != nil {
			log.Fatalf("❌ Ошибка конвертации аудио: %v", err)
		}

		fmt.Printf("✅ Аудио конвертировано: %d байт (%.2f секунд при 24kHz)\n",
			len(audioData), float64(len(audioData))/48000.0)

		// Отправляем аудио чанками (как в оригинале)
		fmt.Println("📤 Отправляем аудио в Yandex Realtime API...")
		chunkSize := 4800 // ~100ms на 24kHz
		if err := client.SendAudioChunked(audioData, chunkSize); err != nil {
			log.Fatalf("❌ Ошибка отправки аудио: %v", err)
		}

		// НЕ отправляем тишину! Server VAD сам определит конец речи
		fmt.Println("⏳ Ждем Server VAD (определение конца речи)...")
		fmt.Println("💡 Server VAD автоматически определит когда речь закончилась")

		// Ждем ответа от API (Server VAD + генерация)
		time.Sleep(20 * time.Second)
	}

	// Дополнительный текстовый запрос (демонстрация прямого вызова)
	fmt.Println("\n📝 Отправляем прямой текстовый запрос...")
	if err := client.TriggerResponse("Какая сегодня погода?"); err != nil {
		log.Printf("❌ Ошибка отправки запроса: %v", err)
	}

	// Ждем ответ
	time.Sleep(10 * time.Second)

	fmt.Println("\n✅ Тест завершен!")
}

// handleAudioOutput обрабатывает аудио ответы
func handleAudioOutput(client *yandex.Client) {
	totalBytes := 0
	for audioChunk := range client.AudioOutput() {
		totalBytes += len(audioChunk)
		fmt.Printf("🔊 Получен аудио чанк: %d байт (всего: %d байт)\n", len(audioChunk), totalBytes)
	}
	fmt.Printf("🔊 Получено аудио данных: %d байт\n", totalBytes)
}

// handleTextOutput обрабатывает текстовые ответы
func handleTextOutput(client *yandex.Client) {
	fullText := ""
	for textChunk := range client.TextOutput() {
		fullText += textChunk
		fmt.Print(textChunk)
	}
	if fullText != "" {
		fmt.Println("\n💬 Полный текстовый ответ получен")
	}
}
