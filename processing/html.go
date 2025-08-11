package processing

import (
	"context"
	"github.com/chromedp/cdproto/page"
	"github.com/rs/zerolog"
	"os"
	"path/filepath"
	"time"
	"watcher/config"

	"github.com/chromedp/chromedp"
)

type Html struct {
	config config.Config
	logger zerolog.Logger
}

func NewHtml(config config.Config, logger zerolog.Logger) *Html {
	return &Html{
		config: config,
		logger: logger,
	}
}

func (h *Html) Execute(filePath string) {
	start := time.Now()
	h.logger.Printf("Start file processing: %s", filePath)

	// Создаем контекст для удаленного Chrome
	allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(),
		h.config.ChromeUrl)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(h.logger.Printf))
	defer cancel()

	var buf []byte
	// Get relative path from watch directory
	relPath, err := filepath.Rel(h.config.WatchDir, filePath)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to get relative path")
		return
	}

	// Construct the path as it appears in the Chrome container
	chromePath := filepath.Join("/watchdir", relPath)
	localURL := "file://" + filepath.ToSlash(chromePath)

	h.logger.Debug().Msgf("Attempting to load URL: %s", localURL)

	// Устанавливаем таймаут
	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Делаем скриншот
	if err := chromedp.Run(ctx, h.fullScreenshot(localURL, 90, &buf)); err != nil {
		h.logger.Error().Err(err).Msg("Screenshot creating error")
	}

	if len(buf) == 0 {
		h.logger.Error().Msg("Screenshot empty")
	}

	outputPath := filepath.Join(filepath.Dir(filePath), "screenshot.png")
	if err := os.WriteFile(outputPath, buf, 0644); err != nil {
		h.logger.Error().Err(err).Msg("Screenshot saving error")
	}

	h.logger.Info().Msgf("File has been processed: %s (time: %v)",
		filepath.Base(filePath), time.Since(start))
}

func (h *Html) fullScreenshot(urlstr string, quality int, res *[]byte) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.Navigate(urlstr),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Получаем размеры страницы
			var width, height int64
			if err := chromedp.Evaluate(`Math.max(
				document.body.scrollWidth,
				document.documentElement.scrollWidth,
				document.body.offsetWidth,
				document.documentElement.offsetWidth,
				document.body.clientWidth,
				document.documentElement.clientWidth
			)`, &width).Do(ctx); err != nil {
				return err
			}

			if err := chromedp.Evaluate(`Math.max(
				document.body.scrollHeight,
				document.documentElement.scrollHeight,
				document.body.offsetHeight,
				document.documentElement.offsetHeight,
				document.body.clientHeight,
				document.documentElement.clientHeight
			)`, &height).Do(ctx); err != nil {
				return err
			}

			// Устанавливаем размер viewport
			if err := chromedp.EmulateViewport(width, height).Do(ctx); err != nil {
				return err
			}

			// Делаем скриншот
			buf, err := page.CaptureScreenshot().
				WithQuality(int64(quality)).
				Do(ctx)
			if err != nil {
				return err
			}

			*res = buf
			return nil
		}),
	}
}
