package processing

import (
	"github.com/rs/zerolog"
	"os"
	"path/filepath"
	"time"
	"watcher/config"
)

type Dummy struct {
	config config.Config
	logger zerolog.Logger
}

func NewDummy(config config.Config, logger zerolog.Logger) *Html {
	return &Html{
		config: config,
		logger: logger,
	}
}

func (h *Dummy) Execute(filePath string) {
	start := time.Now()
	h.logger.Printf("Начало обработки файла: %s", filePath)

	// Имитация обработки
	time.Sleep(h.config.ProcessDelay)

	info, err := os.Stat(filePath)
	if err != nil {
		h.logger.Printf("Ошибка при получении информации о файле %s: %v", filePath, err)
		return
	}

	h.logger.Printf("Файл обработан: %s (размер: %d байт, время: %v)",
		filepath.Base(filePath), info.Size(), time.Since(start))
}
