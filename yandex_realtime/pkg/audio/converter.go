package audio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// ConvertToFormat конвертирует аудио в нужный формат через ffmpeg
func ConvertToFormat(inputFile, outputFile string, sampleRate int, channels int) error {
	cmd := exec.Command("ffmpeg", "-y", "-i", inputFile,
		"-ac", fmt.Sprintf("%d", channels),
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-f", "s16le", // raw PCM16
		"-acodec", "pcm_s16le",
		outputFile)

	return cmd.Run()
}

// ConvertTo24kHz конвертирует аудио в PCM16 24kHz mono для Yandex API
func ConvertTo24kHz(inputFile, outputFile string) error {
	return ConvertToFormat(inputFile, outputFile, 24000, 1)
}

// ReadPCMData читает PCM данные из файла
func ReadPCMData(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

// ConvertAndRead конвертирует аудио файл в PCM 24kHz и возвращает данные
func ConvertAndRead(inputFile string) ([]byte, error) {
	// Создаем временный файл для PCM
	tmpFile, err := os.CreateTemp("", "audio_*.pcm")
	if err != nil {
		return nil, fmt.Errorf("ошибка создания временного файла: %w", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Конвертируем
	if err := ConvertTo24kHz(inputFile, tmpFile.Name()); err != nil {
		return nil, fmt.Errorf("ошибка конвертации: %w", err)
	}

	// Читаем данные
	data, err := ReadPCMData(tmpFile.Name())
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения PCM: %w", err)
	}

	return data, nil
}
