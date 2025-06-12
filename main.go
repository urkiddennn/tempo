package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/faiface/beep/speaker"
)

const (
	sampleRate = 44100
	bufferSize = 512
	musicDir   = "./music"
)

func main() {
	err := speaker.Init(sampleRate, sampleRate/10)
	if err != nil {
		log.Fatalf("Error initializing speaker: %v", err)
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
