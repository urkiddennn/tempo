package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/mjibson/go-dsp/fft"
)

const (
	sampleRate = 44100
	bufferSize = 512
	musicDir   = "./music"
)

func main() {
	// Initialize speaker
	err := speaker.Init(sampleRate, sampleRate/10)
	if err != nil {
		log.Fatalf("Error initializing speaker: %v", err)
	}

	// Scan music directory
	files, err := scanMusicDir(musicDir)
	if err != nil {
		log.Fatalf("Error scanning directory: %v", err)
	}

	// Process each file
	for _, file := range files {
		fmt.Printf("\nPlaying: %s\n", filepath.Base(file))
		err := playAndAnalyze(file)
		if err != nil {
			log.Printf("Error playing %s: %v", file, err)
			continue
		}
	}
}

func scanMusicDir(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(strings.ToLower(info.Name()), ".mp3") || strings.HasSuffix(strings.ToLower(info.Name()), ".wav")) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func playAndAnalyze(file string) error {
	// Open music file
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("error opening file: %v", err)
	}
	defer f.Close()

	// Decode MP3
	streamer, _, err := mp3.Decode(f)
	if err != nil {
		return fmt.Errorf("error decoding file: %v", err)
	}
	defer streamer.Close()

	// Create a custom streamer for analysis
	analyzer := &analyzerStreamer{
		streamer: streamer,
		buffer:   make([][2]float64, bufferSize),
	}

	// Play the stream
	done := make(chan bool)
	speaker.Play(beep.Seq(analyzer, beep.Callback(func() {
		done <- true
	})))

	// Analyze in real-time
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Get latest analysis
			rms, freq := analyzer.getAnalysis()
			// Print results
			printAnalysis(rms, freq)
		case <-done:
			return nil
		}
	}
}

type analyzerStreamer struct {
	streamer beep.Streamer
	buffer   [][2]float64
	rms      float64
	freq     float64
}

func (a *analyzerStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = a.streamer.Stream(samples)
	if !ok {
		return
	}

	// Copy samples to buffer for analysis
	copy(a.buffer, samples)

	// Calculate RMS amplitude
	var sum float64
	for _, sample := range samples {
		// Use left channel (sample[0]) for mono analysis
		sum += sample[0] * sample[0]
	}
	a.rms = math.Sqrt(sum / float64(len(samples)))

	// Perform FFT for frequency analysis
	complexSamples := make([]complex128, len(samples))
	for i, sample := range samples {
		complexSamples[i] = complex(sample[0], 0)
	}
	fftResult := fft.FFT(complexSamples)

	// Calculate magnitude spectrum
	magnitudes := make([]float64, len(fftResult)/2)
	for i := 0; i < len(fftResult)/2; i++ {
		mag := math.Sqrt(real(fftResult[i])*real(fftResult[i]) + imag(fftResult[i])*imag(fftResult[i]))
		magnitudes[i] = mag
	}

	// Find dominant frequency
	maxMag := 0.0
	maxIndex := 0
	for i, mag := range magnitudes {
		if mag > maxMag {
			maxMag = mag
			maxIndex = i
		}
	}
	a.freq = float64(maxIndex) * sampleRate / float64(len(samples))

	return
}

func (a *analyzerStreamer) Err() error {
	return a.streamer.Err()
}

func (a *analyzerStreamer) getAnalysis() (rms, freq float64) {
	return a.rms, a.freq
}

func printAnalysis(rms, freq float64) {
	// Clear terminal (works on Unix-like systems)
	fmt.Print("\033[H\033[2J")
	fmt.Printf("RMS Amplitude: %.4f\n", rms)
	fmt.Printf("Dominant Frequency: %.2f Hz\n", freq)

	// Simple ASCII bar for amplitude
	barLength := int(rms * 50) // Scale for visualization
	if barLength > 50 {
		barLength = 50
	}
	fmt.Print("Amplitude: [")
	for i := 0; i < 50; i++ {
		if i < barLength {
			fmt.Print("=")
		} else {
			fmt.Print(" ")
		}
	}
	fmt.Println("]")
}
