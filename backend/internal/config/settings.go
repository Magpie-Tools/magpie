package config

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

type Config struct {
	Protocols struct {
		HTTP   bool `json:"http"`
		HTTPS  bool `json:"https"`
		Socks4 bool `json:"socks4"`
		Socks5 bool `json:"socks5"`
	} `json:"protocols"`

	Checker struct {
		DynamicThreads bool   `json:"dynamic_threads"`
		Threads        uint32 `json:"threads"`
		MaxThreads     uint32 `json:"max_threads"`
		SaveResponses  bool   `json:"save_responses"`
		Retries        uint32 `json:"retries"`
		Timeout        uint32 `json:"timeout"`
		CheckerTimer   Timer  `json:"checker_timer"`

		JudgesThreads uint32  `json:"judges_threads"`
		JudgesTimeout uint32  `json:"judges_timeout"`
		Judges        []judge `json:"judges"`
		JudgeTimer    Timer   `json:"judge_timer"` // Only for production

		UseHttpsForSocks bool     `json:"use_https_for_socks"`
		IpLookup         string   `json:"ip_lookup"`
		StandardHeader   []string `json:"standard_header"`
		ProxyHeader      []string `json:"proxy_header"`
	} `json:"checker"`

	Scraper struct {
		DynamicThreads bool   `json:"dynamic_threads"`
		Threads        uint32 `json:"threads"`
		MaxThreads     uint32 `json:"max_threads"`
		Retries        uint32 `json:"retries"`
		Timeout        uint32 `json:"timeout"`
		RespectRobots  bool   `json:"respect_robots_txt"`

		ScraperTimer Timer `json:"scraper_timer"`

		ScrapeSites []string `json:"scrape_sites"`
	} `json:"scraper"`

	ProxyLimits ProxyLimitConfig `json:"proxy_limits"`

	Runtime struct {
		ProxyGeoRefreshTimer Timer `json:"proxy_geo_refresh_timer"`
	} `json:"runtime"`

	Plugins PluginConfig `json:"plugins"`

	BlacklistSources []string `json:"blacklist_sources"`
	BlacklistTimer   Timer    `json:"blacklist_timer"`

	WebsiteBlacklist []string `json:"website_blacklist"`
}

type judge struct {
	URL   string `json:"url"`
	Regex string `json:"regex"`
}

type Timer struct {
	Days    uint32 `json:"days"`
	Hours   uint32 `json:"hours"`
	Minutes uint32 `json:"minutes"`
	Seconds uint32 `json:"seconds"`
}

type ProxyLimitConfig struct {
	Enabled       bool   `json:"enabled"`
	MaxPerUser    uint32 `json:"max_per_user"`
	ExcludeAdmins bool   `json:"exclude_admins"`
}

type PluginConfig struct {
	GeoLite   GeoLitePluginConfig   `json:"geolite"`
	AbuseIPDB AbuseIPDBPluginConfig `json:"abuseipdb"`
}

type GeoLitePluginConfig struct {
	Enabled       bool   `json:"enabled"`
	APIKey        string `json:"api_key"`
	AutoUpdate    bool   `json:"auto_update"`
	UpdateTimer   Timer  `json:"update_timer"`
	LastUpdatedAt string `json:"last_updated_at,omitempty"`
}

type AbuseIPDBPluginConfig struct {
	Enabled        bool   `json:"enabled"`
	APIKey         string `json:"api_key"`
	MaxAgeInDays   uint32 `json:"max_age_in_days"`
	DailyLimit     int    `json:"daily_limit"`
	DailyRemaining int    `json:"daily_remaining"`
	DailyResetAt   string `json:"daily_reset_at,omitempty"`
	LastCheckedAt  string `json:"last_checked_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

const settingsFilePath = "data/settings.json"

const (
	settingsDirectoryPath = "data"
	settingsDirectoryMode = 0o700
	settingsFileMode      = 0o600
	minThreadSettingLimit = 1
	defaultThreadFallback = 250
	maxThreadSettingLimit = 2000
)

var (
	//go:embed default_settings.json
	defaultConfig []byte

	configValue atomic.Value
	currentIp   atomic.Value
	configMu    sync.Mutex

	broadcastConfigUpdateFn          = broadcastConfigUpdate
	enforceSettingsFilePermissionsFn = enforceSettingsFilePermissions

	InProductionMode bool
)

func init() {
	// Initialize configValue with a default Config instance
	configValue.Store(Config{})
	currentIp.Store("")
}

func ReadSettings() error {

	data, err := os.ReadFile(settingsFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn("Settings file not found, creating with default configuration")

			err = ensureSettingsStoragePermissions()
			if err != nil {
				return fmt.Errorf("config: ensure settings storage: %w", err)
			}

			err = os.WriteFile(settingsFilePath, defaultConfig, settingsFileMode)
			if err != nil {
				return fmt.Errorf("config: write default settings: %w", err)
			}

			data = defaultConfig
		} else {
			return fmt.Errorf("config: read settings file: %w", err)
		}
	}

	if err := enforceSettingsFilePermissionsFn(); err != nil {
		log.Warn("Error enforcing settings file permissions", "error", err)
	}

	var newConfig Config
	err = json.Unmarshal(data, &newConfig)
	if err != nil {
		return fmt.Errorf("config: parse settings file: %w", err)
	}
	applyLegacyDefaults(data, &newConfig)

	if err := applyConfigUpdate(newConfig, configUpdateOptions{source: "file"}); err != nil {
		return fmt.Errorf("config: apply settings from file: %w", err)
	}

	log.Debug("Settings file loaded successfully")
	return nil
}

func SetConfig(newConfig Config) error {
	if err := applyConfigUpdate(newConfig, configUpdateOptions{persistToFile: true, broadcast: true, source: "local"}); err != nil {
		return fmt.Errorf("config: apply local settings update: %w", err)
	}

	log.Debug("Default Configuration updated and written to file successfully")
	return nil
}

func UpdateGeoLiteConfig(updater func(cfg *Config)) error {
	if updater == nil {
		return errors.New("config: geolite updater cannot be nil")
	}

	cfg := GetConfig()
	updater(&cfg)

	return applyConfigUpdate(cfg, configUpdateOptions{persistToFile: true, broadcast: true, source: "geolite"})
}

func MarkGeoLiteUpdated(ts time.Time) error {
	return UpdateGeoLiteConfig(func(cfg *Config) {
		cfg.Plugins.GeoLite.LastUpdatedAt = ts.UTC().Format(time.RFC3339)
	})
}

func UpdateAbuseIPDBStatus(updater func(cfg *Config)) error {
	if updater == nil {
		return errors.New("config: abuseipdb updater cannot be nil")
	}

	cfg := GetConfig()
	updater(&cfg)

	return applyConfigUpdate(cfg, configUpdateOptions{persistToFile: true, broadcast: true, source: "abuseipdb"})
}

type configUpdateOptions struct {
	persistToFile bool
	broadcast     bool
	source        string
}

func applyConfigUpdate(newConfig Config, opts configUpdateOptions) error {
	configMu.Lock()
	defer configMu.Unlock()

	normalizeThreadSettings(&newConfig)
	newConfig.WebsiteBlacklist = NormalizeWebsiteBlacklist(newConfig.WebsiteBlacklist)

	var errs []error
	var stagedSettingsFilePath string
	cleanupStagedSettingsFile := func() {
		if stagedSettingsFilePath != "" {
			_ = os.Remove(stagedSettingsFilePath)
		}
	}

	if opts.persistToFile {
		data, err := json.MarshalIndent(newConfig, "", "  ")
		if err != nil {
			log.Error("Error marshalling new configuration:", err)
			errs = append(errs, err)
		} else {
			if err := ensureSettingsStoragePermissions(); err != nil {
				log.Error("Error ensuring settings storage permissions:", err)
				errs = append(errs, err)
			} else if stagedFile, err := os.CreateTemp(settingsDirectoryPath, "settings-*.tmp"); err != nil {
				log.Error("Error creating temporary settings file:", err)
				errs = append(errs, err)
			} else {
				stagedSettingsFilePath = stagedFile.Name()
				if _, err := stagedFile.Write(data); err != nil {
					log.Error("Error writing temporary settings file:", err)
					errs = append(errs, err)
				}
				if err := stagedFile.Close(); err != nil {
					log.Error("Error closing temporary settings file:", err)
					errs = append(errs, err)
				}
				if err := os.Chmod(stagedSettingsFilePath, settingsFileMode); err != nil {
					log.Error("Error setting temporary settings file permissions:", err)
					errs = append(errs, err)
				}
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		cleanupStagedSettingsFile()
		return err
	}

	if opts.broadcast {
		payload, err := json.Marshal(newConfig)
		if err != nil {
			log.Error("Error serializing configuration for broadcast:", err)
			errs = append(errs, err)
		} else if err := broadcastConfigUpdateFn(payload); err != nil {
			log.Error("Error broadcasting configuration update:", err)
			errs = append(errs, err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		cleanupStagedSettingsFile()
		return err
	}

	if opts.persistToFile {
		if err := os.Rename(stagedSettingsFilePath, settingsFilePath); err != nil {
			log.Error("Error replacing settings file with staged configuration:", err)
			errs = append(errs, err)
		} else if err := enforceSettingsFilePermissionsFn(); err != nil {
			log.Warn("Settings update persisted but failed to enforce file permissions", "error", err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		cleanupStagedSettingsFile()
		return err
	}

	configValue.Store(newConfig)
	SetBetweenTime()
	updateWebsiteBlocklist(newConfig.WebsiteBlacklist)

	if opts.source != "" {
		log.Debug("Configuration applied", "source", opts.source)
	} else {
		log.Debug("Configuration applied")
	}

	return nil
}

func normalizeThreadSettings(cfg *Config) {
	checkerThreads := cfg.Checker.Threads
	checkerMaxThreads := cfg.Checker.MaxThreads
	cfg.Checker.Threads = clampThreadCount(checkerThreads)
	cfg.Checker.MaxThreads = normalizeMaxThreads(checkerMaxThreads, checkerThreads, defaultThreadFallback, maxThreadSettingLimit)

	scraperThreads := cfg.Scraper.Threads
	scraperMaxThreads := cfg.Scraper.MaxThreads
	cfg.Scraper.Threads = clampThreadCount(scraperThreads)
	cfg.Scraper.MaxThreads = normalizeMaxThreads(scraperMaxThreads, scraperThreads, defaultThreadFallback, maxThreadSettingLimit)

	if cfg.Plugins.AbuseIPDB.MaxAgeInDays == 0 {
		cfg.Plugins.AbuseIPDB.MaxAgeInDays = 30
	}
	if cfg.Plugins.AbuseIPDB.MaxAgeInDays > 365 {
		cfg.Plugins.AbuseIPDB.MaxAgeInDays = 365
	}
}

func normalizeMaxThreads(maxThreads uint32, threads uint32, fallback uint32, cap uint32) uint32 {
	if maxThreads > 0 {
		return clampThreadCountWithCap(maxThreads, cap)
	}
	if threads > 0 {
		return clampThreadCountWithCap(threads, cap)
	}
	return clampThreadCountWithCap(fallback, cap)
}

func clampThreadCount(threads uint32) uint32 {
	return clampThreadCountWithBounds(threads, minThreadSettingLimit, maxThreadSettingLimit)
}

func clampThreadCountWithCap(value uint32, cap uint32) uint32 {
	return clampThreadCountWithBounds(value, minThreadSettingLimit, cap)
}

func clampThreadCountWithBounds(value uint32, min uint32, max uint32) uint32 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func applyLegacyDefaults(raw []byte, cfg *Config) {
	type rawGeoLitePluginConfig struct {
		Enabled       *bool  `json:"enabled"`
		APIKey        string `json:"api_key"`
		AutoUpdate    bool   `json:"auto_update"`
		UpdateTimer   Timer  `json:"update_timer"`
		LastUpdatedAt string `json:"last_updated_at,omitempty"`
	}
	type rawAbuseIPDBPluginConfig struct {
		Enabled        *bool  `json:"enabled"`
		APIKey         string `json:"api_key"`
		MaxAgeInDays   uint32 `json:"max_age_in_days"`
		DailyLimit     int    `json:"daily_limit"`
		DailyRemaining int    `json:"daily_remaining"`
		DailyResetAt   string `json:"daily_reset_at,omitempty"`
		LastCheckedAt  string `json:"last_checked_at,omitempty"`
		LastError      string `json:"last_error,omitempty"`
	}
	normalizeGeoLite := func(rawCfg *rawGeoLitePluginConfig, defaultEnabled bool) (GeoLitePluginConfig, bool) {
		if rawCfg == nil {
			return GeoLitePluginConfig{}, false
		}
		enabled := defaultEnabled
		if rawCfg.Enabled != nil {
			enabled = *rawCfg.Enabled
		}
		return GeoLitePluginConfig{
			Enabled:       enabled,
			APIKey:        rawCfg.APIKey,
			AutoUpdate:    rawCfg.AutoUpdate,
			UpdateTimer:   rawCfg.UpdateTimer,
			LastUpdatedAt: rawCfg.LastUpdatedAt,
		}, true
	}

	var partial struct {
		Checker struct {
			SaveResponses *bool `json:"save_responses"`
		} `json:"checker"`
		Plugins struct {
			GeoLite   *rawGeoLitePluginConfig   `json:"geolite"`
			AbuseIPDB *rawAbuseIPDBPluginConfig `json:"abuseipdb"`
		} `json:"plugins"`
		LegacyGeoLite *rawGeoLitePluginConfig `json:"geolite"`
	}

	if err := json.Unmarshal(raw, &partial); err != nil {
		return
	}

	if partial.Checker.SaveResponses == nil {
		cfg.Checker.SaveResponses = true
	}
	if geolite, ok := normalizeGeoLite(partial.LegacyGeoLite, true); partial.Plugins.GeoLite == nil && ok {
		cfg.Plugins.GeoLite = geolite
	}
	if geolite, ok := normalizeGeoLite(partial.Plugins.GeoLite, false); ok {
		cfg.Plugins.GeoLite = geolite
	}
	if partial.Plugins.AbuseIPDB != nil {
		enabled := false
		if partial.Plugins.AbuseIPDB.Enabled != nil {
			enabled = *partial.Plugins.AbuseIPDB.Enabled
		}
		cfg.Plugins.AbuseIPDB = AbuseIPDBPluginConfig{
			Enabled:        enabled,
			APIKey:         partial.Plugins.AbuseIPDB.APIKey,
			MaxAgeInDays:   partial.Plugins.AbuseIPDB.MaxAgeInDays,
			DailyLimit:     partial.Plugins.AbuseIPDB.DailyLimit,
			DailyRemaining: partial.Plugins.AbuseIPDB.DailyRemaining,
			DailyResetAt:   partial.Plugins.AbuseIPDB.DailyResetAt,
			LastCheckedAt:  partial.Plugins.AbuseIPDB.LastCheckedAt,
			LastError:      partial.Plugins.AbuseIPDB.LastError,
		}
	}
	if cfg.Plugins.AbuseIPDB.MaxAgeInDays == 0 {
		cfg.Plugins.AbuseIPDB.MaxAgeInDays = 30
	}
}

func ensureSettingsStoragePermissions() error {
	if err := os.MkdirAll(settingsDirectoryPath, settingsDirectoryMode); err != nil {
		return err
	}

	if err := os.Chmod(settingsDirectoryPath, settingsDirectoryMode); err != nil {
		return err
	}

	return nil
}

func enforceSettingsFilePermissions() error {
	info, err := os.Stat(settingsFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if info.IsDir() {
		return errors.New("settings path points to a directory")
	}

	if info.Mode().Perm() == settingsFileMode {
		return nil
	}

	return os.Chmod(settingsFilePath, settingsFileMode)
}

func GetConfig() Config {
	// Get the current Config atomically
	return configValue.Load().(Config)
}

func SetProductionMode(productionMode bool) {
	InProductionMode = productionMode
}

func GetCurrentIp() string {
	return currentIp.Load().(string)
}

func SetCurrentIp(ip string) {
	currentIp.Store(ip)
}
