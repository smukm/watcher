package main

import (
	"github.com/fsnotify/fsnotify"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"watcher/config"
	"watcher/processing"
)

/*type Config struct {
	WatchDir      string        `mapstructure:"watchdir"`
	FilePatterns  []string      `mapstructure:"filePatterns"`
	MaxGoroutines int           `mapstructure:"maxGoroutines"`
	ProcessDelay  time.Duration `mapstructure:"processDelay"`
}*/

var cfg config.Config
var configMutex sync.RWMutex
var semaphore chan struct{} // Семафор для ограничения параллельных обработчиков
var fileProcessing processing.ProcessFile

// Добавляем глобальную очередь для отложенных файлов
var (
	pendingFiles chan string
	pendingOnce  sync.Once
)

func init() {
	pendingOnce.Do(func() {
		pendingFiles = make(chan string, 1000) // Буферизированная очередь
	})
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	})
	if err := initConfig(); err != nil {
		log.Fatal().Err(err).Msgf("Failed to initialize config %s", err.Error())
	}
	/*if err := godotenv.Load(); err != nil {
		log.Fatal().Msgf("error loading .env %s", err.Error())
	}*/

	// Инициализируем семафор
	updateSemaphore()

	// Обработка сигналов
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	fileProcessing = processing.NewHtml(cfg, log.Logger)
	// Запуск файлового вотчера
	shutdownChan := make(chan struct{})
	go runFileWatcher(shutdownChan)

	for sig := range sigChan {
		switch sig {
		case syscall.SIGHUP:
			log.Info().Msgf("Received SIGHUP, reloading config")
			if err := initConfig(); err != nil {
				log.Fatal().Err(err).Msgf("Failed to initialize config %s", err.Error())
			}
		case syscall.SIGINT, syscall.SIGTERM:
			log.Info().Msgf("Received signal \"%v\", shutting down", sig)
			close(shutdownChan)
			return
		}
	}

}

// updateSemaphore обновляет семафор при изменении конфигурации
func updateSemaphore() {
	configMutex.RLock()
	maxGoroutines := cfg.MaxGoroutines
	configMutex.RUnlock()

	if semaphore != nil {
		close(semaphore)
	}
	semaphore = make(chan struct{}, maxGoroutines)
}

// runFileWatcher запускает мониторинг директории
func runFileWatcher(shutdownChan <-chan struct{}) {
	// 1. Бесконечный цикл для перезапуска вотчера при ошибках
	for {
		// 2. Неблокирующая проверка канала завершения
		select {
		case <-shutdownChan:
			// 3. Получен сигнал завершения - выходим из функции
			return
		default:
			// 4. Если сигнала нет, запускаем вотчер
			// Получает тот же канал done для вложенной передачи сигнала остановки
			if err := watchFiles(shutdownChan); err != nil {
				log.Error().Err(err).Msg("File watcher error")
				time.Sleep(5 * time.Second)
			}
			// 6. После ошибки цикл начнётся заново
		}
	}
}

// watchFiles выполняет непосредственный мониторинг файлов
func watchFiles(shutdownChan <-chan struct{}) error {
	// 1. Блокируем мьютекс для безопасного чтения конфигурации
	configMutex.RLock()
	// 2. Получаем путь к наблюдаемой директории из конфига
	watchDir := cfg.WatchDir
	// 3. Разблокируем мьютекс после чтения
	configMutex.RUnlock()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(watchDir); err != nil {
		return err
	}

	log.Info().
		Str("directory", watchDir).
		Int("max_goroutines", cap(semaphore)).
		Dur("process_delay", cfg.ProcessDelay).
		Msg("Starting directory monitoring")

	for {
		select {
		case <-shutdownChan:
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if event.Op&fsnotify.Create == fsnotify.Create {
				handleFileEvent(event.Name)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Error().Err(err).Msg("Watcher error")
			return err
		}
	}
}

// handleFileEvent обрабатывает событие создания файла
func handleFileEvent(filePath string) {
	info, err := os.Stat(filePath)
	if err != nil {
		log.Error().Err(err).Str("file", filePath).Msg("Failed to get file info")
		return
	}

	if info.IsDir() {
		return
	}

	if !matchPatterns(filePath) {
		return
	}

	log.Info().Str("file", filePath).Msg("Found matching file")

	select {
	case semaphore <- struct{}{}:
		go func() {
			defer func() { <-semaphore }()
			fileProcessing.Execute(filePath)
			// После обработки проверяем очередь
			checkPendingFiles()
		}()
	default:
		// Вместо пропуска - добавляем в очередь
		log.Warn().Str("file", filePath).Msg("All workers busy, adding to pending queue")
		select {
		case pendingFiles <- filePath:
		default:
			log.Error().Str("file", filePath).Msg("Pending queue full, file dropped")
		}
	}
}

// Новая функция для проверки очереди
func checkPendingFiles() {
	for {
		select {
		case filePath := <-pendingFiles:
			select {
			case semaphore <- struct{}{}:
				go func(f string) {
					defer func() { <-semaphore }()
					fileProcessing.Execute(f)
					checkPendingFiles() // Рекурсивно проверяем ещё
				}(filePath)
				return
			default:
				// Если снова нет места, возвращаем файл в очередь
				select {
				case pendingFiles <- filePath:
					return
				default:
					log.Error().Str("file", filePath).Msg("Failed to requeue pending file")
					return
				}
			}
		default:
			// Очередь пуста
			return
		}
	}
}

// matchPatterns проверяет, соответствует ли файл шаблонам
func matchPatterns(filename string) bool {

	filePatterns := viper.GetStringSlice("filePatterns")
	if len(filePatterns) == 0 {
		return true
	}

	for _, pattern := range filePatterns {
		matched, err := filepath.Match(pattern, filepath.Base(filename))
		if err != nil {
			log.Printf("Ошибка в шаблоне %s: %v", pattern, err)
			continue
		}
		if matched {
			return true
		}
	}
	return false
}

/*func processFile(filePath string) {
	start := time.Now()
	log.Printf("Начало обработки файла: %s", filePath)

	// Имитация обработки
	time.Sleep(cfg.ProcessDelay)

	info, err := os.Stat(filePath)
	if err != nil {
		log.Printf("Ошибка при получении информации о файле %s: %v", filePath, err)
		return
	}

	log.Printf("Файл обработан: %s (размер: %d байт, время: %v)",
		filepath.Base(filePath), info.Size(), time.Since(start))
}*/

// initConfig загружает и валидирует конфигурацию
func initConfig() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("configs")

	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	var newConfig config.Config
	if err := viper.Unmarshal(&newConfig); err != nil {
		return err
	}

	// Валидация конфигурации
	if newConfig.WatchDir == "" {
		newConfig.WatchDir = "."
	}
	if newConfig.MaxGoroutines <= 0 {
		newConfig.MaxGoroutines = 1
	}
	if newConfig.ProcessDelay <= 0 {
		newConfig.ProcessDelay = 1 * time.Second
	} else {
		newConfig.ProcessDelay = newConfig.ProcessDelay * time.Second
	}

	err := envconfig.Process("", &newConfig)
	if err != nil {
		return err
	}

	configMutex.Lock()
	cfg = newConfig
	configMutex.Unlock()

	// Настройка автоматического обновления конфига
	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		log.Info().Str("file", e.Name).Msg("Config file changed, reloading")
		if err := initConfig(); err != nil {
			log.Error().Err(err).Msg("Failed to reload config on change")
		} else {
			updateSemaphore()
		}
	})

	return nil
}
