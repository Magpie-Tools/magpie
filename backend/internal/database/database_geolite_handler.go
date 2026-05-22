package database

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/charmbracelet/log"
	"github.com/oschwald/geoip2-golang"

	"magpie/internal/config"
	"magpie/internal/domain"
)

//go:embed GeoLite2-ASN.mmdb
var geoLiteASNDB []byte

//go:embed GeoLite2-Country.mmdb
var geoLiteCountryDB []byte

type dnsCacheEntry struct {
	names   []string
	expires time.Time
}

type residentialOverrideCandidate struct {
	index int
	ip    string
}

const (
	geoLiteDataDir         = "data/geolite"
	geoLiteASNFilename     = "GeoLite2-ASN.mmdb"
	geoLiteCountryFilename = "GeoLite2-Country.mmdb"
)

const (
	GeoLiteASNFileName     = geoLiteASNFilename
	GeoLiteCountryFileName = geoLiteCountryFilename
)

var (
	countryDB *geoip2.Reader
	asnDB     *geoip2.Reader
	geoLiteMu sync.RWMutex

	datacenterRegex     = regexp.MustCompile(`(?i)(amazon|google|microsoft|digitalocean|linode|hetzner|ovh|vultr|ibm|alibaba|tencent|cloudflare|rackspace|hostinger|upcloud|azure|gcp|aws)`)
	residentialKeywords = regexp.MustCompile(`(?i)(dyn|pool|dsl|cust|res|ip|adsl|ppp|user|mobile|static|dhcp)`)
	ispKeywords         = regexp.MustCompile(`(?i)(isp|broadband|telecom|communications|networks|carrier)`)

	dnsCache                      sync.Map
	dnsLookupGroup                singleflight.Group
	dnsCacheTTL                   = 12 * time.Hour
	dnsLookupTimeout              = 2 * time.Second
	maxEnrichmentWorkers          = 64
	maxResidentialOverrideWorkers = 32
	enrichmentUpdateBatchSize     = 512
)

func init() {
	if err := loadGeoLiteDatabases(true); err != nil {
		log.Warn("GeoLite databases initialized with embedded fallback", "error", err)
	}
}

func loadGeoLiteDatabases(startup bool) error {
	geoLiteMu.Lock()
	defer geoLiteMu.Unlock()

	var (
		errorList     []error
		countryReader *geoip2.Reader
		asnReader     *geoip2.Reader
	)

	if reader, err := readerFromDisk(geoLiteCountryFilename); err == nil {
		countryReader = reader
	} else {
		errorList = append(errorList, fmt.Errorf("country: %w", err))
		if startup && len(geoLiteCountryDB) > 0 {
			if fallbackReader, fallbackErr := geoip2.FromBytes(geoLiteCountryDB); fallbackErr == nil {
				countryReader = fallbackReader
			} else {
				errorList = append(errorList, fmt.Errorf("country fallback: %w", fallbackErr))
			}
		}
	}

	if reader, err := readerFromDisk(geoLiteASNFilename); err == nil {
		asnReader = reader
	} else {
		errorList = append(errorList, fmt.Errorf("asn: %w", err))
		if startup && len(geoLiteASNDB) > 0 {
			if fallbackReader, fallbackErr := geoip2.FromBytes(geoLiteASNDB); fallbackErr == nil {
				asnReader = fallbackReader
			} else {
				errorList = append(errorList, fmt.Errorf("asn fallback: %w", fallbackErr))
			}
		}
	}

	if countryReader == nil || asnReader == nil {
		if len(errorList) == 0 {
			return errors.New("geolite databases unavailable")
		}
		return errors.Join(errorList...)
	}

	oldCountry := countryDB
	oldASN := asnDB
	countryDB = countryReader
	asnDB = asnReader

	if oldCountry != nil {
		_ = oldCountry.Close()
	}
	if oldASN != nil {
		_ = oldASN.Close()
	}

	return nil
}

func GeoLiteFilePath(filename string) string {
	return filepath.Join(geoLiteDataDir, filename)
}

func EnsureGeoLiteDataDir() error {
	return os.MkdirAll(geoLiteDataDir, 0o755)
}

func readerFromDisk(filename string) (*geoip2.Reader, error) {
	path := GeoLiteFilePath(filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return geoip2.FromBytes(data)
}

func ReloadGeoLiteFromDisk() error {
	return loadGeoLiteDatabases(false)
}

func GeoLiteAvailable() bool {
	if !config.GetConfig().Plugins.GeoLite.Enabled {
		return false
	}

	geoLiteMu.RLock()
	defer geoLiteMu.RUnlock()
	return countryDB != nil && asnDB != nil
}

func getCachedDNS(ip string) []string {
	now := time.Now()
	if entry, ok := dnsCache.Load(ip); ok {
		cachedEntry := entry.(dnsCacheEntry)
		if now.Before(cachedEntry.expires) {
			return cachedEntry.names
		}
	}

	result, err, _ := dnsLookupGroup.Do(ip, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(context.Background(), dnsLookupTimeout)
		defer cancel()

		names, err := net.DefaultResolver.LookupAddr(ctx, ip)
		if err != nil {
			return []string{}, nil // Cache failures as empty results
		}
		return names, nil
	})

	if err != nil {
		result = []string{}
	}

	names := result.([]string)
	dnsCache.Store(ip, dnsCacheEntry{
		names:   names,
		expires: now.Add(dnsCacheTTL),
	})
	return names
}

func AsyncEnrichProxyMetadata(proxies []domain.Proxy) {
	if len(proxies) == 0 {
		return
	}

	copySlice := make([]domain.Proxy, len(proxies))
	copy(copySlice, proxies)

	go func(items []domain.Proxy) {
		candidates := EnrichProxiesWithCountryAndType(&items)
		if err := persistProxyMetadata(items); err != nil {
			log.Error("persist proxy metadata", "err", err)
			return
		}
		if len(candidates) == 0 {
			return
		}
		if err := applyResidentialOverrides(&items, candidates); err != nil {
			log.Error("apply residential overrides", "err", err)
		}
	}(copySlice)
}

func EnrichProxiesWithCountryAndType(proxies *[]domain.Proxy) []residentialOverrideCandidate {
	if proxies == nil || len(*proxies) == 0 {
		return nil
	}
	if !config.GetConfig().Plugins.GeoLite.Enabled {
		return nil
	}

	workerCount := runtime.NumCPU() * 4
	if workerCount > maxEnrichmentWorkers {
		workerCount = maxEnrichmentWorkers
	}
	if workerCount < 1 {
		workerCount = 1
	}
	if len(*proxies) < workerCount {
		workerCount = len(*proxies)
	}

	jobs := make(chan int, workerCount)
	candidateCh := make(chan residentialOverrideCandidate, len(*proxies))
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				proxy := &(*proxies)[idx]
				ip := proxy.GetIp()
				proxy.Country = GetCountryCode(ip)
				typeValue, needsDNS := determineProxyTypeByASN(ip)
				proxy.EstimatedType = typeValue
				if needsDNS {
					candidateCh <- residentialOverrideCandidate{index: idx, ip: ip}
				}
			}
		}()
	}

	for i := range *proxies {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(candidateCh)

	candidates := make([]residentialOverrideCandidate, 0, len(*proxies))
	for candidate := range candidateCh {
		candidates = append(candidates, candidate)
	}

	return candidates
}

func determineProxyTypeByASN(ipAddress string) (string, bool) {
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return "unknown", false
	}
	if !config.GetConfig().Plugins.GeoLite.Enabled {
		return "unknown", false
	}

	geoLiteMu.RLock()
	defer geoLiteMu.RUnlock()
	if asnDB == nil {
		return "unknown", false
	}

	asnRecord, err := asnDB.ASN(ip)
	if err != nil {
		return "unknown", true
	}

	org := strings.ToLower(asnRecord.AutonomousSystemOrganization)
	switch {
	case strings.Contains(org, "customer") || strings.Contains(org, "residential"):
		return "Residential", false
	case datacenterRegex.MatchString(org):
		return "Datacenter", true
	case ispKeywords.MatchString(org):
		return "ISP", true
	default:
		return "N/A", true
	}
}

func applyResidentialOverrides(proxies *[]domain.Proxy, candidates []residentialOverrideCandidate) error {
	if proxies == nil || len(*proxies) == 0 || len(candidates) == 0 {
		return nil
	}

	ipToIndices := make(map[string][]int, len(candidates))
	for _, candidate := range candidates {
		if candidate.index < 0 || candidate.index >= len(*proxies) {
			continue
		}
		ipToIndices[candidate.ip] = append(ipToIndices[candidate.ip], candidate.index)
	}

	if len(ipToIndices) == 0 {
		return nil
	}

	type dnsJob struct {
		ip      string
		indices []int
	}

	jobs := make(chan dnsJob)
	var wg sync.WaitGroup
	var mu sync.Mutex
	overrides := make([]domain.Proxy, 0, len(candidates))

	workerCount := len(ipToIndices)
	if workerCount > maxResidentialOverrideWorkers {
		workerCount = maxResidentialOverrideWorkers
	}
	if workerCount < 1 {
		workerCount = 1
	}

	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			for job := range jobs {
				names := getCachedDNS(job.ip)
				var matchesResidential bool
				for _, name := range names {
					if residentialKeywords.MatchString(name) {
						matchesResidential = true
						break
					}
				}
				if !matchesResidential {
					continue
				}

				mu.Lock()
				for _, idx := range job.indices {
					if idx < 0 || idx >= len(*proxies) {
						continue
					}
					proxy := &(*proxies)[idx]
					if proxy.EstimatedType == "Residential" {
						continue
					}
					proxy.EstimatedType = "Residential"
					overrides = append(overrides, *proxy)
				}
				mu.Unlock()
			}
		}()
	}

	for ip, indices := range ipToIndices {
		jobs <- dnsJob{ip: ip, indices: indices}
	}
	close(jobs)
	wg.Wait()

	if len(overrides) == 0 {
		return nil
	}

	return persistProxyMetadata(overrides)
}

func FilterProxiesMissingGeo(proxies []domain.Proxy) []domain.Proxy {
	if len(proxies) == 0 {
		return nil
	}

	filtered := make([]domain.Proxy, 0, len(proxies))
	for _, proxy := range proxies {
		country := strings.TrimSpace(proxy.Country)
		proxyType := strings.TrimSpace(proxy.EstimatedType)

		if country == "" || strings.EqualFold(country, "n/a") ||
			proxyType == "" || strings.EqualFold(proxyType, "n/a") ||
			strings.EqualFold(proxyType, "unknown") {
			filtered = append(filtered, proxy)
		}
	}
	return filtered
}

func persistProxyMetadata(proxies []domain.Proxy) error {
	for i := 0; i < len(proxies); i += enrichmentUpdateBatchSize {
		end := i + enrichmentUpdateBatchSize
		if end > len(proxies) {
			end = len(proxies)
		}
		batch := proxies[i:end]
		if err := updateProxyMetadataBatch(batch); err != nil {
			return err
		}
	}
	return nil
}

func updateProxyMetadataBatch(batch []domain.Proxy) error {
	if len(batch) == 0 {
		return nil
	}

	values := make([]string, len(batch))
	args := make([]interface{}, 0, len(batch)*3)
	for i, proxy := range batch {
		values[i] = "(?::bigint, ?::text, ?::text)"
		args = append(args, proxy.ID, proxy.Country, proxy.EstimatedType)
	}

	query := fmt.Sprintf(`UPDATE proxies AS p
SET country = tmp.country,
    estimated_type = tmp.estimated_type
FROM (VALUES %s) AS tmp(id, country, estimated_type)
WHERE p.id = tmp.id`, strings.Join(values, ","))

	return DB.Exec(query, args...).Error
}

func GetCountryCode(ipAddress string) string {
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return "N/A"
	}
	if !config.GetConfig().Plugins.GeoLite.Enabled {
		return "N/A"
	}

	geoLiteMu.RLock()
	defer geoLiteMu.RUnlock()
	if countryDB == nil {
		return "N/A"
	}

	record, err := countryDB.Country(ip)
	if err != nil {
		return "N/A"
	}

	if name := record.Country.Names["en"]; name != "" {
		return name
	}

	if record.Country.IsoCode != "" {
		return strings.ToUpper(record.Country.IsoCode)
	}

	return "N/A"
}

func DetermineProxyType(ipAddress string) string {
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return "unknown"
	}
	if !config.GetConfig().Plugins.GeoLite.Enabled {
		return "unknown"
	}

	// Check cached reverse DNS results
	names := getCachedDNS(ipAddress)
	for _, name := range names {
		if residentialKeywords.MatchString(name) {
			return "Residential"
		}
	}

	geoLiteMu.RLock()
	if asnDB == nil {
		geoLiteMu.RUnlock()
		return "unknown"
	}
	asnRecord, err := asnDB.ASN(ip)
	geoLiteMu.RUnlock()
	if err != nil {
		return "unknown"
	}

	org := strings.ToLower(asnRecord.AutonomousSystemOrganization)

	// Check for datacenter organizations
	if datacenterRegex.MatchString(org) {
		return "Datacenter"
	}

	// Check for ISP indicators in ASN organization
	if ispKeywords.MatchString(org) {
		return "ISP"
	}

	// Final check for common residential ASN patterns
	if strings.Contains(org, "customer") || strings.Contains(org, "residential") {
		return "Residential"
	}

	// Default to ISP for unknown organizations
	return "N/A"
}
