package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/mjibson/go-dsp/fft"
)

const (
	sampleRate        = 44100     // Standard audio sample rate
	bufferSize        = 2048      // Increased buffer size for better FFT resolution
	numFrequencyBands = 30        // Number of bars in our spectrum analyzer
	maxBarHeight      = 15        // Maximum height of each bar in rows
	musicDir          = "./music" // Directory where your music files are located
)

// model represents the state of our terminal UI application.
type model struct {
	spectrum []float64 // Stores magnitudes for each frequency band
	quitting bool      // Flag to indicate if the program is in the process of quitting
}

// updateMsg is a custom message type used to send audio analysis data to the TUI.
type updateMsg struct {
	spectrum []float64
}

// Init is the first function called when the program starts.
func (m model) Init() tea.Cmd {
	return nil
}

// Update handles incoming messages and updates the model's state.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle key presses for quitting the application.
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.quitting = true
			return m, tea.Quit
		}
	case updateMsg:
		// Update the model with the latest spectrum data.
		m.spectrum = msg.spectrum
	}
	return m, nil
}

// View renders the current state of the model to the terminal.
func (m model) View() string {
	// Style for the bars.
	barStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00FF00")) // Bright Green

	var barsBuilder strings.Builder
	// Build a column of bars for each frequency band.
	// We rotate it by building column by column using characters, then joining rows.
	for h := maxBarHeight; h >= 0; h-- { // Iterate from top to bottom row
		for _, bandMagnitude := range m.spectrum {
			// Normalize magnitude to bar height
			barHeight := int(bandMagnitude * float64(maxBarHeight))
			if barHeight < 0 {
				barHeight = 0
			}
			if barHeight > maxBarHeight {
				barHeight = maxBarHeight
			}

			if barHeight >= h { // If current bar extends to this height
				barsBuilder.WriteString("█") // Filled block
			} else {
				barsBuilder.WriteString(" ") // Empty space
			}
			barsBuilder.WriteString(" ") // Space between bars for separation
		}
		barsBuilder.WriteString("\n") // New line for the next row
	}

	finalView := barStyle.Render(barsBuilder.String())

	// Add quitting message if applicable.
	if m.quitting {
		finalView += "\n\nGoodbye!\n"
	} else {
		finalView += "\nPress q or Ctrl+C to quit\n"
	}

	return finalView
}

func main() {
	err := speaker.Init(sampleRate, sampleRate/10)
	if err != nil {
		log.Fatalf("Error initializing speaker: %v", err)
	}
	defer speaker.Close()

	p := tea.NewProgram(model{}, tea.WithOutput(os.Stdout))

	go func() {
		if err := p.Start(); err != nil {
			log.Fatalf("Error starting TUI: %v", err)
		}
		os.Exit(0)
	}()

	files, err := scanMusicDir(musicDir)
	if err != nil {
		log.Fatalf("Error scanning music directory '%s': %v", musicDir, err)
	}

	if len(files) == 0 {
		log.Printf("No music files found in '%s'. Please add some MP3/WAV files.\n", musicDir)
		time.Sleep(2 * time.Second)
		p.Send(tea.Quit())
		os.Exit(0)
	}

	for _, file := range files {
		err := playAndAnalyze(file, p)
		if err != nil {
			log.Printf("Error playing %s: %v", file, err)
			continue
		}
	}

	p.Send(tea.Quit())
}

func scanMusicDir(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			name := strings.ToLower(info.Name())
			if strings.HasSuffix(name, ".mp3") || strings.HasSuffix(name, ".wav") {
				files = append(files, path)
			}
		}
		return nil
	})
	return files, err
}

func playAndAnalyze(file string, p *tea.Program) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("error opening file: %v", err)
	}
	defer f.Close()

	var streamer beep.Streamer
	var format beep.Format

	if strings.HasSuffix(strings.ToLower(file), ".mp3") {
		streamer, format, err = mp3.Decode(f)
	} else if strings.HasSuffix(strings.ToLower(file), ".wav") {
		// WAV decoding not included in original example, but can be added with "github.com/faiface/beep/wav"
		return fmt.Errorf("WAV decoding not implemented in this example. Please use MP3 files.")
	} else {
		return fmt.Errorf("unsupported file format for %s", file)
	}

	if err != nil {
		return fmt.Errorf("error decoding file: %v", err)
	}

	if format.SampleRate != sampleRate {
		streamer = beep.Resample(3, format.SampleRate, sampleRate, streamer)
	}

	analyzer := &analyzerStreamer{
		streamer: streamer,
		buffer:   make([][2]float64, bufferSize),
		spectrum: make([]float64, numFrequencyBands), // Initialize spectrum slice
	}

	done := make(chan bool)
	speaker.Play(beep.Seq(analyzer, beep.Callback(func() {
		done <- true
	})))

	ticker := time.NewTicker(50 * time.Millisecond) // Faster updates for smoother visualization
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Get the latest spectrum data.
			spectrum := analyzer.getSpectrum()
			// Send an update message to the Bubble Tea program.
			p.Send(updateMsg{spectrum: spectrum})
		case <-done:
			return nil
		}
	}
}

// analyzerStreamer wraps an existing beep.Streamer to perform real-time audio analysis.
type analyzerStreamer struct {
	streamer beep.Streamer
	buffer   [][2]float64 // Buffer to hold audio samples for analysis
	spectrum []float64    // Stores magnitudes for each frequency band
}

// Stream reads audio samples from the wrapped streamer and performs analysis.
func (a *analyzerStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = a.streamer.Stream(samples)
	if !ok {
		return
	}

	// Only process up to `bufferSize` samples. FFT works best with power-of-2 sizes.
	// If `n` (actual samples read) is less than `bufferSize`, use `n`.
	processSize := n
	if processSize > bufferSize {
		processSize = bufferSize
	}

	// Copy samples to our internal buffer for FFT.
	copy(a.buffer, samples[:processSize])

	// Perform FFT for frequency analysis
	complexSamples := make([]complex128, processSize)
	for i := 0; i < processSize; i++ {
		// Use the left channel for FFT input.
		complexSamples[i] = complex(samples[i][0], 0)
	}

	fftResult := fft.FFT(complexSamples)

	// Calculate magnitudes for each frequency bin
	magnitudes := make([]float64, len(fftResult)/2) // Only positive frequencies
	for i := 0; i < len(fftResult)/2; i++ {
		mag := math.Sqrt(real(fftResult[i])*real(fftResult[i]) + imag(fftResult[i])*imag(fftResult[i]))
		magnitudes[i] = mag
	}

	// Map FFT magnitudes to frequency bands for visualization.
	// This is a simplified approach, a more sophisticated mapping might use logarithmic scales.
	if len(magnitudes) == 0 {
		return
	}

	// Reset spectrum values
	for i := range a.spectrum {
		a.spectrum[i] = 0.0
	}

	// Determine how many FFT bins per display bar.
	binsPerBand := len(magnitudes) / numFrequencyBands
	if binsPerBand == 0 { // Ensure there's at least one bin per band
		binsPerBand = 1
	}

	for i := 0; i < numFrequencyBands; i++ {
		bandSum := 0.0
		binCount := 0
		// Sum magnitudes for bins within this band.
		for j := i * binsPerBand; j < (i+1)*binsPerBand && j < len(magnitudes); j++ {
			// Apply a simple logarithmic scaling to magnitudes for better visual dynamic range.
			// This helps to make lower frequencies more visible.
			bandSum += 20 * math.Log10(magnitudes[j]+1e-6) // +1e-6 to avoid log(0)
			binCount++
		}
		if binCount > 0 {
			// Average the magnitudes for the band and normalize.
			// The normalization factor might need adjustment based on desired visual intensity.
			normalizedMag := (bandSum / float64(binCount)) / 100.0 // Adjust divisor to scale for display
			if normalizedMag < 0 {
				normalizedMag = 0
			}
			a.spectrum[i] = normalizedMag
		}
	}
	// A simple smoothing to make the bars less jumpy
	for i := range a.spectrum {
		if i > 0 {
			a.spectrum[i] = (a.spectrum[i] + a.spectrum[i-1]*0.5) / 1.5 // Average with neighbor
		}
	}

	return
}

// Err returns any error encountered by the underlying streamer.
func (a *analyzerStreamer) Err() error {
	return a.streamer.Err()
}

// getSpectrum provides the most recently calculated frequency spectrum.
func (a *analyzerStreamer) getSpectrum() []float64 {
	// Return a copy to prevent concurrent modification issues.
	tempSpectrum := make([]float64, len(a.spectrum))
	copy(tempSpectrum, a.spectrum)
	return tempSpectrum
}
