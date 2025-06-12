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
	sampleRate = 44100
	bufferSize = 512
	musicDir   = "./music"
)

type model struct {
	currentSong string
	rms         float64
	freq        float64
}

type updateMsg struct {
	rms         float64
	freq        float64
	currentSong string
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return m, tea.Quit
		}
	case updateMsg:
		m.currentSong = msg.currentSong
		m.rms = msg.rms
		m.freq = msg.freq
	}
	return m, nil
}

func (m model) View() string {
	// Styles
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF6347")).
		Padding(0, 1)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFD700")).
		Padding(0, 1)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7D56F4"))

	barStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00FF00")).Width(20).
		Padding(0, 1)

	// Build vertical amplitude bar
	maxBarHeight := 20 // Maximum height of the bar in rows
	barHeight := int(m.rms * float64(maxBarHeight))
	if barHeight > maxBarHeight {
		barHeight = maxBarHeight
	}
	bar := strings.Repeat("█\n", barHeight) + strings.Repeat(" \n", maxBarHeight-barHeight)
	styledBar := barStyle.Render("Amplitude\n" + bar)

	// Text content
	textContent := lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Render("Real-Time Audio Analysis"),
		labelStyle.Render("Current Song: ")+valueStyle.Render(m.currentSong),
		labelStyle.Render("RMS Amplitude: ")+valueStyle.Render(fmt.Sprintf("%.4f", m.rms)),
		labelStyle.Render("Dominant Frequency: ")+valueStyle.Render(fmt.Sprintf("%.2f Hz", m.freq)),
		"\nPress q or Ctrl+C to quit",
	)

	// Combine text and bar horizontally
	return lipgloss.JoinHorizontal(lipgloss.Top, textContent, styledBar)
}

func main() {
	// Initialize speaker
	err := speaker.Init(sampleRate, sampleRate/10)
	if err != nil {
		log.Fatalf("Error initializing speaker: %v", err)
	}

	// Initialize Bubble Tea program
	p := tea.NewProgram(model{currentSong: "None"}, tea.WithOutput(os.Stdout))
	go func() {
		if err := p.Start(); err != nil {
			log.Fatalf("Error starting TUI: %v", err)
		}
		os.Exit(0)
	}()

	// Scan music directory
	files, err := scanMusicDir(musicDir)
	if err != nil {
		log.Fatalf("Error scanning directory: %v", err)
	}

	// Process each file
	for _, file := range files {
		// Update TUI with current song
		p.Send(updateMsg{currentSong: filepath.Base(file)})
		err := playAndAnalyze(file, p)
		if err != nil {
			log.Printf("Error playing %s: %v", file, err)
			continue
		}
	}

	// Quit TUI when done
	p.Send(tea.Quit())
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

func playAndAnalyze(file string, p *tea.Program) error {
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
			// Update TUI
			p.Send(updateMsg{
				rms:         rms,
				freq:        freq,
				currentSong: filepath.Base(file),
			})
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
