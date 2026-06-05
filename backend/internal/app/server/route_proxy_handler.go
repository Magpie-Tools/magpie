package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"magpie/internal/api/dto"
	"magpie/internal/auth"
	"magpie/internal/blacklist"
	"magpie/internal/database"
	"magpie/internal/domain"
	proxyqueue "magpie/internal/jobs/queue/proxy"
	"magpie/internal/support"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"gorm.io/gorm"
)

const (
	proxyUploadParseChunkBytes = 1 << 20
	proxyUploadBatchSize       = 5_000
	proxyUploadMaxLineBytes    = 1 << 20
	proxyExportBatchSize       = 2_000
)

var errMissingProxyUploadContent = errors.New("missing proxy upload content")
var getQueuedProxyForUser = database.GetQueuedProxyForUser
var removeQueuedProxies = func(proxies []domain.Proxy) error {
	return proxyqueue.PublicProxyQueue.RemoveFromQueue(proxies)
}
var enqueueProxiesNow = func(proxies []domain.Proxy) error {
	return proxyqueue.PublicProxyQueue.AddToQueue(proxies)
}

func addProxies(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	startedAt := time.Now()
	maxBodyBytes := resolveUploadMaxBodyBytes()
	insertedCount, parseStats, blacklistedCount, err := ingestProxyUploadMultipart(w, r, userID, maxBodyBytes)
	if err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			writeError(w, "Input line exceeds maximum supported length", http.StatusRequestEntityTooLarge)
			return
		}
		if isRequestBodyTooLarge(err) {
			writeError(w, requestBodyTooLargeMessage(maxBodyBytes), http.StatusRequestEntityTooLarge)
			return
		}
		if errors.Is(err, errMissingProxyUploadContent) {
			writeError(w, "Failed to retrieve file", http.StatusBadRequest)
			return
		}
		log.Error("Could not add proxies to database", "error", err)
		writeError(w, "Could not add proxies to database", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	processingMs := time.Since(startedAt).Milliseconds()
	response := dto.AddProxiesResponse{
		ProxyCount: insertedCount,
		Details: dto.AddProxiesDetails{
			SubmittedCount:     parseStats.SubmittedCount,
			ParsedCount:        parseStats.ParsedCount,
			InvalidFormatCount: parseStats.InvalidFormatCount,
			InvalidIPCount:     parseStats.InvalidIPCount,
			InvalidIPv4Count:   parseStats.InvalidIPv4Count,
			InvalidPortCount:   parseStats.InvalidPortCount,
			BlacklistedCount:   blacklistedCount,
			ProcessingMs:       processingMs,
		},
	}
	json.NewEncoder(w).Encode(response)
}

func ingestProxyUploadMultipart(w http.ResponseWriter, r *http.Request, userID uint, maxBodyBytes int64) (int, support.ProxyParseStats, int, error) {
	if r == nil || r.Body == nil {
		return 0, support.ProxyParseStats{}, 0, errMissingProxyUploadContent
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	multipartReader, err := r.MultipartReader()
	if err != nil {
		return 0, support.ProxyParseStats{}, 0, err
	}

	var (
		stats          support.ProxyParseStats
		insertedCount  int
		blacklistCount int
		batch          []domain.Proxy
		sawInput       bool
	)

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}

		filtered, blocked := blacklist.FilterProxies(batch)
		blacklistCount += len(blocked)
		if len(blocked) > 0 {
			log.Info("Dropped blacklisted proxies from upload", "count", len(blocked))
		}

		batch = batch[:0]
		if len(filtered) == 0 {
			return nil
		}

		inserted, err := database.InsertAndGetProxiesWithUser(filtered, userID)
		if err != nil {
			return err
		}

		if len(inserted) > 0 {
			insertedCount += len(inserted)
			database.AsyncEnrichProxyMetadata(inserted)
			if err := proxyqueue.PublicProxyQueue.AddToQueue(inserted); err != nil {
				log.Error("Could not add proxies to queue", "error", err)
			}
		}

		return nil
	}

	flushParseChunk := func(chunk *strings.Builder) error {
		if chunk.Len() == 0 {
			return nil
		}

		parsed, parseStats := support.ParseTextToProxiesWithStats(chunk.String())
		stats.SubmittedCount += parseStats.SubmittedCount
		stats.ParsedCount += parseStats.ParsedCount
		stats.InvalidFormatCount += parseStats.InvalidFormatCount
		stats.InvalidIPCount += parseStats.InvalidIPCount
		stats.InvalidIPv4Count += parseStats.InvalidIPv4Count
		stats.InvalidPortCount += parseStats.InvalidPortCount

		batch = append(batch, parsed...)
		chunk.Reset()

		if len(batch) >= proxyUploadBatchSize {
			return flushBatch()
		}
		return nil
	}

	ingestPart := func(source io.Reader) error {
		scanner := bufio.NewScanner(source)
		scanner.Buffer(make([]byte, 64*1024), proxyUploadMaxLineBytes)
		var parseChunk strings.Builder

		for scanner.Scan() {
			parseChunk.WriteString(scanner.Text())
			parseChunk.WriteByte('\n')
			if parseChunk.Len() >= proxyUploadParseChunkBytes {
				if err := flushParseChunk(&parseChunk); err != nil {
					return err
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		return flushParseChunk(&parseChunk)
	}

	for {
		part, err := multipartReader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, support.ProxyParseStats{}, 0, err
		}

		formName := strings.TrimSpace(part.FormName())
		switch formName {
		case "file", "proxyTextarea", "clipboardProxies":
			sawInput = true
			if name := strings.TrimSpace(part.FileName()); name != "" {
				log.Debugf("Uploaded file: %s", name)
			}
			if err := ingestPart(part); err != nil {
				_ = part.Close()
				return 0, support.ProxyParseStats{}, 0, err
			}
		default:
			if _, err := io.Copy(io.Discard, part); err != nil {
				_ = part.Close()
				return 0, support.ProxyParseStats{}, 0, err
			}
		}

		if err := part.Close(); err != nil {
			return 0, support.ProxyParseStats{}, 0, err
		}
	}

	if !sawInput {
		return 0, support.ProxyParseStats{}, 0, errMissingProxyUploadContent
	}
	if err := flushBatch(); err != nil {
		return 0, support.ProxyParseStats{}, 0, err
	}

	return insertedCount, stats, blacklistCount, nil
}

func getProxyPage(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	page, err := strconv.Atoi(r.PathValue("page"))
	if err != nil {
		log.Error("error converting page to int", "error", err.Error())
		writeError(w, "Invalid page", http.StatusBadRequest)
		return
	}

	pageSize := 0
	if rawPageSize := r.URL.Query().Get("pageSize"); rawPageSize != "" {
		if parsedPageSize, parseErr := strconv.Atoi(rawPageSize); parseErr == nil && parsedPageSize > 0 {
			pageSize = parsedPageSize
		}
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))

	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if status != "alive" && status != "dead" {
		status = ""
	}

	filters := dto.ProxyListFilters{
		Status:           status,
		Protocols:        normalizeQueryList(r.URL.Query()["protocol"]),
		MinHealthOverall: parseHealthPercentParam(r.URL.Query().Get("minHealthOverall")),
		MinHealthHTTP:    parseHealthPercentParam(r.URL.Query().Get("minHealthHttp")),
		MinHealthHTTPS:   parseHealthPercentParam(r.URL.Query().Get("minHealthHttps")),
		MinHealthSOCKS4:  parseHealthPercentParam(r.URL.Query().Get("minHealthSocks4")),
		MinHealthSOCKS5:  parseHealthPercentParam(r.URL.Query().Get("minHealthSocks5")),
		Countries:        normalizeQueryList(r.URL.Query()["country"]),
		Types:            normalizeQueryList(r.URL.Query()["type"]),
		AnonymityLevels:  normalizeQueryList(r.URL.Query()["anonymity"]),
		MaxTimeout:       parsePositiveIntParam(r.URL.Query().Get("maxTimeout")),
		MaxRetries:       parsePositiveIntParam(r.URL.Query().Get("maxRetries")),
		ReputationLabels: normalizeQueryList(r.URL.Query()["reputation"]),
	}

	includeHealth := parseBoolQueryParam(r.URL.Query().Get("includeHealth"), true)
	includeReputation := parseBoolQueryParam(r.URL.Query().Get("includeReputation"), true)
	sortField := strings.TrimSpace(r.URL.Query().Get("sortField"))
	sortOrder := strings.TrimSpace(r.URL.Query().Get("sortOrder"))

	proxies, total := database.GetProxyInfoPageWithFiltersAndOptions(
		userID,
		page,
		pageSize,
		search,
		filters,
		database.ProxyPageQueryOptions{
			IncludeHealth:     includeHealth,
			IncludeReputation: includeReputation,
			SortField:         sortField,
			SortOrder:         sortOrder,
		},
	)

	response := dto.ProxyPage{
		Proxies: proxies,
		Total:   total,
	}

	json.NewEncoder(w).Encode(response)
}

func getProxyFilters(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	options, err := database.GetProxyFilterOptions(userID)
	if err != nil {
		log.Error("error retrieving proxy filters", "error", err.Error())
		writeError(w, "Failed to retrieve proxy filters", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(options)
}

func normalizeQueryList(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if _, exists := seen[lower]; exists {
			continue
		}
		seen[lower] = struct{}{}
		normalized = append(normalized, lower)
	}

	return normalized
}

func parsePositiveIntParam(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func parseHealthPercentParam(value string) int {
	parsed := parsePositiveIntParam(value)
	if parsed > 100 {
		return 100
	}
	return parsed
}

func parseBoolQueryParam(value string, defaultValue bool) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return defaultValue
	}

	switch trimmed {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defaultValue
	}
}

func getProxyCount(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	json.NewEncoder(w).Encode(database.GetAllProxyCountOfUser(userID))
}

func getProxyDetail(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	proxyID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		log.Error("error converting proxy id", "error", err.Error())
		writeError(w, "Invalid proxy id", http.StatusBadRequest)
		return
	}

	detail, dbErr := database.GetProxyDetail(userID, proxyID)
	if dbErr != nil {
		log.Error("error retrieving proxy detail", "error", dbErr.Error(), "proxy_id", proxyID)
		writeError(w, "Failed to retrieve proxy", http.StatusInternalServerError)
		return
	}

	if detail == nil {
		writeError(w, "Proxy not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(detail)
}

func requeueProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	proxyID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		log.Error("error converting proxy id", "error", err.Error())
		writeError(w, "Invalid proxy id", http.StatusBadRequest)
		return
	}

	proxy, dbErr := getQueuedProxyForUser(userID, proxyID)
	if dbErr != nil {
		log.Error("error retrieving proxy for requeue", "error", dbErr.Error(), "proxy_id", proxyID, "user_id", userID)
		writeError(w, "Failed to retrieve proxy", http.StatusInternalServerError)
		return
	}

	if proxy == nil {
		writeError(w, "Proxy not found", http.StatusNotFound)
		return
	}

	if err := removeQueuedProxies([]domain.Proxy{*proxy}); err != nil {
		log.Error("failed to remove proxy from queue before requeue", "error", err, "proxy_id", proxyID, "user_id", userID)
		writeError(w, "Failed to queue proxy", http.StatusServiceUnavailable)
		return
	}

	if err := enqueueProxiesNow([]domain.Proxy{*proxy}); err != nil {
		log.Error("failed to queue proxy", "error", err, "proxy_id", proxyID, "user_id", userID)
		writeError(w, "Failed to queue proxy", http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "Proxy queued successfully",
		"proxy_id": proxyID,
	})
}

func getProxyStatistics(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	proxyID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		log.Error("error converting proxy id", "error", err.Error())
		writeError(w, "Invalid proxy id", http.StatusBadRequest)
		return
	}

	limit := 100
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if parsed, parseErr := strconv.Atoi(rawLimit); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}

	statistics, dbErr := database.GetProxyStatistics(userID, proxyID, limit)
	if dbErr != nil {
		log.Error("error retrieving proxy statistics", "error", dbErr.Error(), "proxy_id", proxyID)
		writeError(w, "Failed to retrieve proxy statistics", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"statistics": statistics})
}

func getProxyStatisticResponseBody(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	proxyID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		log.Error("error converting proxy id", "error", err.Error())
		writeError(w, "Invalid proxy id", http.StatusBadRequest)
		return
	}

	statisticID, err := strconv.ParseUint(r.PathValue("statisticId"), 10, 64)
	if err != nil {
		log.Error("error converting statistic id", "error", err.Error())
		writeError(w, "Invalid statistic id", http.StatusBadRequest)
		return
	}

	responseDetail, dbErr := database.GetProxyStatisticResponseBody(userID, proxyID, statisticID)
	if dbErr != nil {
		if errors.Is(dbErr, gorm.ErrRecordNotFound) {
			writeError(w, "Proxy statistic not found", http.StatusNotFound)
			return
		}

		log.Error("error retrieving proxy statistic body", "error", dbErr.Error(), "proxy_id", proxyID, "statistic_id", statisticID)
		writeError(w, "Failed to retrieve proxy statistic body", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(responseDetail)
}

func deleteProxies(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body json.RawMessage
	if !decodeJSONBodyLimited(w, r, &body, resolveJSONMaxBodyBytes()) {
		return
	}
	body = bytes.TrimSpace(body)

	if len(body) == 0 {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if body[0] == '[' {
		var proxies []int
		if err := json.Unmarshal(body, &proxies); err != nil {
			writeError(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if len(proxies) == 0 {
			writeError(w, "No proxies selected for deletion", http.StatusBadRequest)
			return
		}

		deleted, orphaned, deleteErr := database.DeleteProxyRelation(userID, proxies)
		if deleteErr != nil {
			log.Error("could not delete proxies", "error", deleteErr.Error())
			writeError(w, "Could not delete proxies", http.StatusInternalServerError)
			return
		}

		if len(orphaned) > 0 {
			if err := proxyqueue.PublicProxyQueue.RemoveFromQueue(orphaned); err != nil {
				log.Error("failed to remove orphaned proxies from queue", "error", err)
			}
		}

		if deleted == 0 {
			json.NewEncoder(w).Encode("No proxies matched the delete criteria.")
			return
		}

		json.NewEncoder(w).Encode(fmt.Sprintf("Deleted %d proxies.", deleted))
		return
	}

	var settings dto.DeleteSettings
	if err := json.Unmarshal(body, &settings); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if settings.Scope != "all" && settings.Scope != "selected" {
		settings.Scope = "all"
	}

	deleted, orphaned, deleteErr := database.DeleteProxiesWithSettings(userID, settings)
	if deleteErr != nil {
		if errors.Is(deleteErr, database.ErrNoProxiesSelected) {
			writeError(w, "No proxies selected for deletion", http.StatusBadRequest)
			return
		}

		log.Error("could not delete proxies with filters", "error", deleteErr.Error())
		writeError(w, "Could not delete proxies", http.StatusInternalServerError)
		return
	}

	if len(orphaned) > 0 {
		if err := proxyqueue.PublicProxyQueue.RemoveFromQueue(orphaned); err != nil {
			log.Error("failed to remove orphaned proxies from queue", "error", err)
		}
	}

	if deleted == 0 {
		json.NewEncoder(w).Encode("No proxies matched the delete criteria.")
		return
	}

	json.NewEncoder(w).Encode(fmt.Sprintf("Deleted %d proxies.", deleted))
}

func exportProxies(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var settings dto.ExportSettings
	if !decodeJSONBodyLimited(w, r, &settings, resolveJSONMaxBodyBytes()) {
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", "attachment; filename=proxies.txt")
	writer := bufio.NewWriterSize(w, 256*1024)
	wroteBytes := false

	err := database.StreamProxiesForExport(userID, settings, proxyExportBatchSize, func(proxy domain.Proxy) error {
		line := support.FormatProxy(proxy, settings.OutputFormat)
		if _, err := writer.WriteString(line); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		wroteBytes = true
		return nil
	})
	if err != nil {
		handleExportProxiesStreamError(w, err, wroteBytes)
		return
	}

	if err := writer.Flush(); err != nil {
		log.Warn("export proxies flush failed", "error", err)
	}
}

func handleExportProxiesStreamError(w http.ResponseWriter, err error, wroteBytes bool) {
	log.Error("export proxies stream failed", "error", err)
	if wroteBytes {
		return
	}
	writeError(w, "Could not export proxies", http.StatusInternalServerError)
}
