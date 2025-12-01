package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// ConvertToFormat конвертирует аудио в нужный формат через ffmpeg
func ConvertToFormat(inputFile, outputFile string, sampleRate int, channels int) error {
	cmd := exec.Command("ffmpeg", "-y", "-i", inputFile,
		"-ac", fmt.Sprintf("%d", channels),
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-sample_fmt", "s16",
		outputFile)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// ConvertTo8kHz конвертирует аудио в PCM16 8kHz mono для Asterisk
func ConvertTo8kHz(inputFile, outputFile string) error {
	return ConvertToFormat(inputFile, outputFile, 8000, 1)
}

// ConvertTo24kHz конвертирует аудио в PCM16 24kHz mono для Yandex API
func ConvertTo24kHz(inputFile, outputFile string) error {
	return ConvertToFormat(inputFile, outputFile, 24000, 1)
}

// ReadWAVData читает аудио данные из WAV файла
func ReadWAVData(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return nil, fmt.Errorf("невалидный WAV файл")
	}

	if err := decoder.FwdToPCM(); err != nil {
		return nil, fmt.Errorf("ошибка поиска PCM данных: %w", err)
	}

	duration, err := decoder.Duration()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения длительности: %w", err)
	}

	bufferSize := int(decoder.NumChans) * int(duration.Seconds()*float64(decoder.SampleRate))

	buf := &audio.IntBuffer{
		Data:   make([]int, bufferSize),
		Format: &audio.Format{SampleRate: int(decoder.SampleRate), NumChannels: int(decoder.NumChans)},
	}

	n, err := decoder.PCMBuffer(buf)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения PCM: %w", err)
	}

	// Конвертируем int samples в PCM16 (little endian bytes)
	audioBytes := make([]byte, n*2)
	for i := 0; i < n; i++ {
		sample := int16(buf.Data[i])
		audioBytes[i*2] = byte(sample & 0xFF)
		audioBytes[i*2+1] = byte((sample >> 8) & 0xFF)
	}

	return audioBytes, nil
}

// SaveWAV сохраняет аудио данные в WAV файл
func SaveWAV(filename string, chunks [][]byte, sampleRate int) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Собираем все данные
	var totalBytes []byte
	for _, chunk := range chunks {
		totalBytes = append(totalBytes, chunk...)
	}

	// Конвертируем bytes в int samples
	numSamples := len(totalBytes) / 2
	samples := make([]int, numSamples)
	for i := 0; i < numSamples; i++ {
		sample := int16(totalBytes[i*2]) | (int16(totalBytes[i*2+1]) << 8)
		samples[i] = int(sample)
	}

	buf := &audio.IntBuffer{
		Data: samples,
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: 1,
		},
	}

	encoder := wav.NewEncoder(file, sampleRate, 16, 1, 1)
	defer encoder.Close()

	if err := encoder.Write(buf); err != nil {
		return fmt.Errorf("ошибка записи WAV: %w", err)
	}

	return nil
}

// SaveWAVFromSamples сохраняет PCM16 сэмплы как WAV файл
func SaveWAVFromSamples(filename string, samples []int16, sampleRate int) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	numSamples := len(samples)
	dataSize := numSamples * 2

	// RIFF заголовок
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataSize))
	f.Write([]byte("WAVE"))

	// fmt chunk
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint32(sampleRate))
	binary.Write(f, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(f, binary.LittleEndian, uint16(2))
	binary.Write(f, binary.LittleEndian, uint16(16))

	// data chunk
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, uint32(dataSize))

	return binary.Write(f, binary.LittleEndian, samples)
}

// MakeSilence создает чанк тишины
func MakeSilence(samples int) []byte {
	return make([]byte, samples*2) // 2 байта на sample (PCM16)
}

// StreamingResampler represents a streaming audio resampler
type StreamingResampler struct {
	fromRate   int
	toRate     int
	ratio      float64
	lastSample int16 // For linear interpolation continuity
}

// NewStreamingResampler creates a new streaming resampler
func NewStreamingResampler(fromRate, toRate int) *StreamingResampler {
	return &StreamingResampler{
		fromRate:   fromRate,
		toRate:     toRate,
		ratio:      float64(toRate) / float64(fromRate),
		lastSample: 0,
	}
}

// Resample resamples PCM16 audio data using linear interpolation
func (r *StreamingResampler) Resample(inputData []byte) ([]byte, error) {
	if len(inputData) == 0 {
		return []byte{}, nil
	}

	// Convert bytes to int16 samples
	inputSamples := make([]int16, len(inputData)/2)
	for i := 0; i < len(inputSamples); i++ {
		inputSamples[i] = int16(binary.LittleEndian.Uint16(inputData[i*2:]))
	}

	// Calculate output length
	outputLen := int(float64(len(inputSamples)) * r.ratio)
	outputSamples := make([]int16, outputLen)

	// Linear interpolation resampling
	for i := 0; i < outputLen; i++ {
		// Calculate position in input array
		srcPos := float64(i) / r.ratio
		srcIndex := int(srcPos)
		srcFrac := srcPos - float64(srcIndex)

		// Get samples for interpolation
		var sample0, sample1 int16
		if srcIndex < len(inputSamples) {
			sample0 = inputSamples[srcIndex]
		} else {
			sample0 = r.lastSample
		}

		if srcIndex+1 < len(inputSamples) {
			sample1 = inputSamples[srcIndex+1]
		} else {
			sample1 = r.lastSample
		}

		// Linear interpolation
		interpolated := float64(sample0)*(1.0-srcFrac) + float64(sample1)*srcFrac
		outputSamples[i] = int16(interpolated)
	}

	// Save last sample for next chunk
	if len(inputSamples) > 0 {
		r.lastSample = inputSamples[len(inputSamples)-1]
	}

	// Convert int16 samples back to bytes
	outputData := make([]byte, len(outputSamples)*2)
	for i, sample := range outputSamples {
		binary.LittleEndian.PutUint16(outputData[i*2:], uint16(sample))
	}

	return outputData, nil
}

// Close closes the resampler (no-op for pure Go implementation, for API compatibility)
func (r *StreamingResampler) Close() error {
	return nil
}

// ResampleAudio ресемплирует аудио из одного формата в другой через ffmpeg
// DEPRECATED: Use NewStreamingResampler for streaming use cases
// This function is kept for compatibility with file-based operations
func ResampleAudio(inputData []byte, fromRate, toRate int) ([]byte, error) {
	// For backward compatibility, use streaming resampler
	resampler := NewStreamingResampler(fromRate, toRate)
	return resampler.Resample(inputData)
}

// SplitWAVIntoChunks разбивает WAV файл на чанки
func SplitWAVIntoChunks(wavPath string, chunkDurationSec int, outputDir string) ([]string, error) {
	f, err := os.Open(wavPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Читаем WAV заголовок
	header := make([]byte, 44)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}

	// Читаем все аудио данные
	audioData, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	// Получаем параметры из заголовка
	sampleRate := int(binary.LittleEndian.Uint32(header[24:28]))
	bytesPerSec := sampleRate * 2 // 16-bit mono

	chunkSize := bytesPerSec * chunkDurationSec
	var chunks []string

	for i := 0; i*chunkSize < len(audioData); i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(audioData) {
			end = len(audioData)
		}

		chunkPath := fmt.Sprintf("%s/chunk-%03d.wav", outputDir, i+1)
		if err := saveWAVChunk(chunkPath, header, audioData[start:end]); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunkPath)
	}

	return chunks, nil
}

func saveWAVChunk(path string, header, audioData []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dataSize := uint32(len(audioData))
	fileSize := 36 + dataSize

	newHeader := make([]byte, 44)
	copy(newHeader, header)

	binary.LittleEndian.PutUint32(newHeader[4:8], fileSize)
	binary.LittleEndian.PutUint32(newHeader[40:44], dataSize)

	if _, err := f.Write(newHeader); err != nil {
		return err
	}
	if _, err := f.Write(audioData); err != nil {
		return err
	}

	return nil
}
