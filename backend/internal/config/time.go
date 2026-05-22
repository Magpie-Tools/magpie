package config

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultProxyGeoRefreshInterval  = 24 * time.Hour
	defaultGeoLiteUpdateInterval    = 24 * time.Hour
	defaultBlacklistRefreshInterval = 6 * time.Hour
)

var (
	timeBetweenChecks          atomic.Value
	timeBetweenScrapes         atomic.Value
	blacklistRefreshInterval   atomic.Value
	proxyGeoRefreshInterval    atomic.Value
	checkIntervalListeners     []chan time.Duration
	scrapeIntervalListeners    []chan time.Duration
	blacklistIntervalListeners []chan time.Duration
	proxyGeoRefreshListeners   []chan time.Duration
	geoLiteUpdateInterval      atomic.Value
	geoLiteUpdateListeners     []chan time.Duration
	listenersMu                sync.Mutex
)

func init() {
	timeBetweenChecks.Store(time.Second)
	timeBetweenScrapes.Store(time.Second)
	blacklistRefreshInterval.Store(time.Second)
	proxyGeoRefreshInterval.Store(defaultProxyGeoRefreshInterval)
	geoLiteUpdateInterval.Store(defaultGeoLiteUpdateInterval)
}

func SetBetweenTime() {
	cfg := GetConfig()
	setTimeBetweenChecks(CalculateBetweenTime(cfg.Checker.CheckerTimer))
	setTimeBetweenScrapes(CalculateBetweenTime(cfg.Scraper.ScraperTimer))
	setBlacklistRefreshInterval(calculateBlacklistRefreshInterval(cfg))
	setProxyGeoRefreshInterval(calculateProxyGeoRefreshInterval(cfg))
	setGeoLiteUpdateInterval(calculateGeoLiteUpdateInterval(cfg))
}

// CalculateBetweenTime Also works with e.g a judgeCount
func CalculateBetweenTime(timer Timer) time.Duration {
	intervalMs := CalculateMillisecondsOfCheckingPeriod(timer)

	// Enforce minimum interval (e.g., 1 second)
	minInterval := uint64(1000)
	if intervalMs < minInterval {
		intervalMs = minInterval
	}

	return time.Duration(intervalMs) * time.Millisecond
}

func CalculateMillisecondsOfCheckingPeriod(timer Timer) uint64 {
	// Calculate total duration in milliseconds
	return uint64(timer.Days)*24*60*60*1000 +
		uint64(timer.Hours)*60*60*1000 +
		uint64(timer.Minutes)*60*1000 +
		uint64(timer.Seconds)*1000
}

func GetTimeBetweenChecks() time.Duration {
	return timeBetweenChecks.Load().(time.Duration)
}

func CheckIntervalUpdates() <-chan time.Duration {
	ch := make(chan time.Duration, 1)
	listenersMu.Lock()
	checkIntervalListeners = append(checkIntervalListeners, ch)
	listenersMu.Unlock()

	ch <- GetTimeBetweenChecks()
	return ch
}

func setTimeBetweenChecks(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}

	current := GetTimeBetweenChecks()
	if current == interval {
		return
	}

	timeBetweenChecks.Store(interval)

	listenersMu.Lock()
	defer listenersMu.Unlock()
	for _, ch := range checkIntervalListeners {
		select {
		case ch <- interval:
		default:
		}
	}
}

func setTimeBetweenScrapes(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}

	current := GetTimeBetweenScrapes()
	if current == interval {
		return
	}

	timeBetweenScrapes.Store(interval)

	listenersMu.Lock()
	defer listenersMu.Unlock()
	for _, ch := range scrapeIntervalListeners {
		select {
		case ch <- interval:
		default:
		}
	}
}

func GetTimeBetweenScrapes() time.Duration {
	return timeBetweenScrapes.Load().(time.Duration)
}

func ScrapeIntervalUpdates() <-chan time.Duration {
	ch := make(chan time.Duration, 1)
	listenersMu.Lock()
	scrapeIntervalListeners = append(scrapeIntervalListeners, ch)
	listenersMu.Unlock()

	ch <- GetTimeBetweenScrapes()
	return ch
}

func setBlacklistRefreshInterval(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}

	current := GetBlacklistRefreshInterval()
	if current == interval {
		return
	}

	blacklistRefreshInterval.Store(interval)

	listenersMu.Lock()
	defer listenersMu.Unlock()
	for _, ch := range blacklistIntervalListeners {
		select {
		case ch <- interval:
		default:
		}
	}
}

func GetBlacklistRefreshInterval() time.Duration {
	return blacklistRefreshInterval.Load().(time.Duration)
}

func BlacklistIntervalUpdates() <-chan time.Duration {
	ch := make(chan time.Duration, 1)
	listenersMu.Lock()
	blacklistIntervalListeners = append(blacklistIntervalListeners, ch)
	listenersMu.Unlock()

	ch <- GetBlacklistRefreshInterval()
	return ch
}

func GetProxyGeoRefreshInterval() time.Duration {
	return proxyGeoRefreshInterval.Load().(time.Duration)
}

func calculateBlacklistRefreshInterval(cfg Config) time.Duration {
	timer := cfg.BlacklistTimer
	if timer.Days == 0 && timer.Hours == 0 && timer.Minutes == 0 && timer.Seconds == 0 {
		return defaultBlacklistRefreshInterval
	}
	return CalculateBetweenTime(timer)
}

func ProxyGeoRefreshIntervalUpdates() <-chan time.Duration {
	ch := make(chan time.Duration, 1)
	listenersMu.Lock()
	proxyGeoRefreshListeners = append(proxyGeoRefreshListeners, ch)
	listenersMu.Unlock()

	ch <- GetProxyGeoRefreshInterval()
	return ch
}

func setProxyGeoRefreshInterval(interval time.Duration) {
	if interval <= 0 {
		interval = defaultProxyGeoRefreshInterval
	}
	current := GetProxyGeoRefreshInterval()
	if current == interval {
		return
	}
	proxyGeoRefreshInterval.Store(interval)

	listenersMu.Lock()
	defer listenersMu.Unlock()
	for _, ch := range proxyGeoRefreshListeners {
		select {
		case ch <- interval:
		default:
		}
	}
}

func calculateProxyGeoRefreshInterval(cfg Config) time.Duration {
	timer := cfg.Runtime.ProxyGeoRefreshTimer
	if timer.Days == 0 && timer.Hours == 0 && timer.Minutes == 0 && timer.Seconds == 0 {
		return defaultProxyGeoRefreshInterval
	}
	return CalculateBetweenTime(timer)
}

func GetGeoLiteUpdateInterval() time.Duration {
	return geoLiteUpdateInterval.Load().(time.Duration)
}

func GeoLiteUpdateIntervalUpdates() <-chan time.Duration {
	ch := make(chan time.Duration, 1)
	listenersMu.Lock()
	geoLiteUpdateListeners = append(geoLiteUpdateListeners, ch)
	listenersMu.Unlock()

	ch <- GetGeoLiteUpdateInterval()
	return ch
}

func setGeoLiteUpdateInterval(interval time.Duration) {
	if interval <= 0 {
		interval = defaultGeoLiteUpdateInterval
	}
	current := GetGeoLiteUpdateInterval()
	if current == interval {
		return
	}
	geoLiteUpdateInterval.Store(interval)

	listenersMu.Lock()
	defer listenersMu.Unlock()
	for _, ch := range geoLiteUpdateListeners {
		select {
		case ch <- interval:
		default:
		}
	}
}

func calculateGeoLiteUpdateInterval(cfg Config) time.Duration {
	timer := cfg.Plugins.GeoLite.UpdateTimer
	if timer.Days == 0 && timer.Hours == 0 && timer.Minutes == 0 && timer.Seconds == 0 {
		return defaultGeoLiteUpdateInterval
	}
	return CalculateBetweenTime(timer)
}
