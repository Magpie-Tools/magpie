package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"magpie/internal/api/dto"
	"magpie/internal/auth"
	"magpie/internal/config"
	"magpie/internal/database"
	"magpie/internal/domain"
	sitequeue "magpie/internal/jobs/queue/sites"
	"magpie/internal/jobs/scraper"
	"magpie/internal/support"

	"github.com/charmbracelet/log"
)

func getScrapeSourcesCount(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	filters := parseScrapeSourceListFilters(r)
	json.NewEncoder(w).Encode(database.GetAllScrapeSiteCountOfUserWithSearchAndFilters(userID, search, filters))
}

func getScrapeSourcePage(w http.ResponseWriter, r *http.Request) {
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

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	filters := parseScrapeSourceListFilters(r)
	scrapeSiteInfoPages := database.GetScrapeSiteInfoPageWithSearchAndFilters(userID, page, search, filters)

	json.NewEncoder(w).Encode(scrapeSiteInfoPages)
}

func parseScrapeSourceListFilters(r *http.Request) dto.ScrapeSourceListFilters {
	return dto.ScrapeSourceListFilters{
		Protocols:          normalizeQueryList(r.URL.Query()["protocol"]),
		ProxyCountOperator: normalizeCountOperator(r.URL.Query().Get("proxyCountOperator")),
		ProxyCount:         uint(parsePositiveIntParam(r.URL.Query().Get("proxyCount"))),
		AliveCountOperator: normalizeCountOperator(r.URL.Query().Get("aliveCountOperator")),
		AliveCount:         uint(parsePositiveIntParam(r.URL.Query().Get("aliveCount"))),
	}
}

func normalizeCountOperator(operator string) string {
	if strings.TrimSpace(operator) == "<" {
		return "<"
	}
	return ">"
}

func exportScrapeSources(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var settings dto.ScrapeSourceExportSettings
	if !decodeJSONBodyLimited(w, r, &settings, resolveJSONMaxBodyBytes()) {
		return
	}

	sources, dbErr := database.GetScrapeSiteInfoForExport(userID, settings)
	if dbErr != nil {
		log.Error("error exporting scrape sources", "error", dbErr.Error())
		writeError(w, "Could not export scrape sources", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", "attachment; filename=scrape-sources.txt")
	writer := bufio.NewWriterSize(w, 64*1024)
	for _, source := range sources {
		line := support.FormatScrapeSource(source, settings.OutputFormat)
		if _, err := writer.WriteString(line); err != nil {
			log.Error("export scrape sources write failed", "error", err)
			return
		}
		if err := writer.WriteByte('\n'); err != nil {
			log.Error("export scrape sources newline write failed", "error", err)
			return
		}
	}
	if err := writer.Flush(); err != nil {
		log.Warn("export scrape sources flush failed", "error", err)
	}
}

func getScrapeSourceProxies(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sourceID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		log.Error("error converting scrape source id", "error", err.Error())
		writeError(w, "Invalid scrape source id", http.StatusBadRequest)
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(r.URL.Query().Get("page")); rawPage != "" {
		if parsed, parseErr := strconv.Atoi(rawPage); parseErr == nil && parsed > 0 {
			page = parsed
		}
	}

	pageSize := 0
	if rawPageSize := strings.TrimSpace(r.URL.Query().Get("pageSize")); rawPageSize != "" {
		if parsed, parseErr := strconv.Atoi(rawPageSize); parseErr == nil && parsed > 0 {
			pageSize = parsed
		}
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	sortField := strings.TrimSpace(r.URL.Query().Get("sortField"))
	sortOrder := strings.TrimSpace(r.URL.Query().Get("sortOrder"))
	includeHealth := parseBoolQueryParam(r.URL.Query().Get("includeHealth"), true)
	includeReputation := parseBoolQueryParam(r.URL.Query().Get("includeReputation"), true)

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

	proxies, total, dbErr := database.GetScrapeSiteProxyPageWithOptions(userID, sourceID, page, pageSize, search, filters, database.ProxyPageQueryOptions{
		IncludeHealth:     includeHealth,
		IncludeReputation: includeReputation,
		SortField:         sortField,
		SortOrder:         sortOrder,
	})
	if dbErr != nil {
		log.Error("error retrieving scrape source proxies", "error", dbErr.Error(), "scrape_source_id", sourceID)
		writeError(w, "Failed to retrieve scrape source proxies", http.StatusInternalServerError)
		return
	}

	response := dto.ProxyPage{
		Proxies: proxies,
		Total:   total,
	}

	json.NewEncoder(w).Encode(response)
}

func getScrapeSourceDetail(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sourceID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		log.Error("error converting scrape source id", "error", err.Error())
		writeError(w, "Invalid scrape source id", http.StatusBadRequest)
		return
	}

	detail, dbErr := database.GetScrapeSiteDetail(userID, sourceID)
	if dbErr != nil {
		log.Error("error retrieving scrape source detail", "error", dbErr.Error(), "scrape_source_id", sourceID)
		writeError(w, "Failed to retrieve scrape source", http.StatusInternalServerError)
		return
	}

	if detail == nil {
		writeError(w, "Scrape source not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(detail)
}

func requeueScrapeSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sourceID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		log.Error("error converting scrape source id", "error", err.Error())
		writeError(w, "Invalid scrape source id", http.StatusBadRequest)
		return
	}

	detail, dbErr := getScrapeSourceDetailForUser(userID, sourceID)
	if dbErr != nil {
		log.Error("error retrieving scrape source for requeue", "error", dbErr.Error(), "scrape_source_id", sourceID, "user_id", userID)
		writeError(w, "Failed to retrieve scrape source", http.StatusInternalServerError)
		return
	}

	if detail == nil {
		writeError(w, "Scrape source not found", http.StatusNotFound)
		return
	}

	site := domain.ScrapeSite{
		ID:  detail.Id,
		URL: detail.Url,
	}

	if err := removeScrapeSitesFromQueue([]domain.ScrapeSite{site}); err != nil {
		log.Error("failed to remove scrape source from queue before requeue", "error", err, "scrape_source_id", sourceID, "user_id", userID)
		writeError(w, "Failed to queue scrape source", http.StatusServiceUnavailable)
		return
	}

	if err := enqueueScrapeSites([]domain.ScrapeSite{site}); err != nil {
		log.Error("failed to queue scrape source", "error", err, "scrape_source_id", sourceID, "user_id", userID)
		writeError(w, "Failed to queue scrape source", http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "Scrape source queued successfully",
		"source_id": sourceID,
	})
}

func deleteScrapingSources(w http.ResponseWriter, r *http.Request) {
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
		var scrapingSource []int
		if err := json.Unmarshal(body, &scrapingSource); err != nil {
			writeError(w, "Invalid request", http.StatusBadRequest)
			return
		}

		deleteScrapingSourcesByIDs(w, userID, scrapingSource)
		return
	}

	var settings dto.ScrapeSourceDeleteSettings
	if err := json.Unmarshal(body, &settings); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if settings.Scope != "all" && settings.Scope != "selected" {
		settings.Scope = "all"
	}

	deleted, orphaned, deleteErr := database.DeleteScrapeSitesWithSettings(userID, settings)
	if deleteErr != nil {
		if errors.Is(deleteErr, database.ErrNoScrapeSourcesSelected) {
			writeError(w, "No scraping sources selected for deletion", http.StatusBadRequest)
			return
		}

		log.Error("could not delete scrape sites with filters", "error", deleteErr.Error())
		writeError(w, "Could not delete scraping sources", http.StatusInternalServerError)
		return
	}

	writeScrapeSourceDeleteResponse(w, deleted, orphaned)
}

func deleteScrapingSourcesByIDs(w http.ResponseWriter, userID uint, scrapingSource []int) {
	deleted, orphaned, deleteErr := database.DeleteScrapeSiteRelation(userID, scrapingSource)
	if deleteErr != nil {
		log.Error("could not delete scrape sites", "error", deleteErr.Error())
		writeError(w, "Could not delete scraping sources", http.StatusInternalServerError)
		return
	}

	writeScrapeSourceDeleteResponse(w, deleted, orphaned)
}

func writeScrapeSourceDeleteResponse(w http.ResponseWriter, deleted int64, orphaned []domain.ScrapeSite) {
	if len(orphaned) > 0 {
		if err := sitequeue.PublicScrapeSiteQueue.RemoveFromQueue(orphaned); err != nil {
			log.Error("failed to remove scrape sites from queue", "error", err, "count", len(orphaned))
		}
	}

	if deleted == 0 {
		json.NewEncoder(w).Encode("No scraping sources matched the delete criteria.")
		return
	}

	json.NewEncoder(w).Encode(fmt.Sprintf("Deleted %d scraping sources.", deleted))
}

func saveScrapingSources(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserIDFromRequest(r)
	if err != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	maxBodyBytes := resolveUploadMaxBodyBytes()
	if !prepareMultipartForm(w, r, maxBodyBytes, resolveMultipartMemoryBytes()) {
		return
	}

	textareaContent := r.FormValue("scrapeSourceTextarea") // Match the key sent by frontend
	clipboardContent := r.FormValue("clipboardScrapeSources")
	file, fileHeader, err := r.FormFile("file") // "file" is the key of the form field

	var fileContent []byte

	if err == nil {
		defer file.Close()

		log.Debugf("Uploaded file: %s (%d bytes)", fileHeader.Filename, fileHeader.Size)

		fileContent, err = io.ReadAll(file)
		if err != nil {
			if isRequestBodyTooLarge(err) {
				writeError(w, requestBodyTooLargeMessage(maxBodyBytes), http.StatusRequestEntityTooLarge)
				return
			}
			writeError(w, "Failed to read file", http.StatusInternalServerError)
			return
		}
	} else if !errors.Is(err, http.ErrMissingFile) {
		writeError(w, "Failed to retrieve file", http.StatusBadRequest)
		return
	} else if len(textareaContent) == 0 && len(clipboardContent) == 0 {
		writeError(w, "Failed to retrieve sources from any input method", http.StatusBadRequest)
		return
	}

	// Merge the file content and the textarea content
	mergedContent := string(fileContent) + "\n" + textareaContent + "\n" + clipboardContent

	log.Infof("Sources content received: %d bytes", len(mergedContent))

	// Parse the merged content into a slice of sources
	sources := support.ParseTextToSources(mergedContent)

	var allowedSources, blockedSources, unsafeSources []string
	for _, src := range sources {
		if _, err := support.ValidateOutboundHTTPLiteral(src); err != nil {
			unsafeSources = append(unsafeSources, src)
			continue
		}
		if config.IsWebsiteBlocked(src) {
			blockedSources = append(blockedSources, src)
			continue
		}
		allowedSources = append(allowedSources, src)
	}

	if len(blockedSources) > 0 || len(unsafeSources) > 0 {
		payload := map[string]any{
			"error": "One or more scrape sources are not allowed",
		}
		if len(blockedSources) > 0 {
			payload["blocked_sources"] = dedupeStrings(blockedSources)
			payload["websiteBlacklist"] = config.GetConfig().WebsiteBlacklist
		}
		if len(unsafeSources) > 0 {
			payload["unsafe_sources"] = dedupeStrings(unsafeSources)
		}

		writeJSON(w, http.StatusBadRequest, payload)
		return
	}

	sites, err := database.SaveScrapingSourcesOfUsers(userID, allowedSources)
	if err != nil {
		log.Error("Could not save sources to database", "error", err.Error())
		writeError(w, "Could not save sources to database", http.StatusInternalServerError)
		return
	}

	if err := enqueueScrapeSitesOrRollback(userID, sites); err != nil {
		log.Error("Could not queue scrape sources", "user_id", userID, "error", err)
		writeError(w, "Could not queue scrape sources; saved changes were rolled back", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]int{"sourceCount": len(sites)})
}

func checkScrapeSourceRobots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if _, err := auth.GetUserIDFromRequest(r); err != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" {
		writeError(w, "Missing url query parameter", http.StatusBadRequest)
		return
	}
	if config.IsWebsiteBlocked(rawURL) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":            "This website is blocked",
			"blocked_website":  rawURL,
			"websiteBlacklist": config.GetConfig().WebsiteBlacklist,
		})
		return
	}
	if _, err := support.ValidateOutboundHTTPURLContext(r.Context(), rawURL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "URL points to a disallowed network target",
		})
		return
	}

	result, err := scraper.CheckRobotsAllowance(rawURL, 10*time.Second)
	if err != nil {
		log.Warn("robots.txt check", "url", rawURL, "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"allowed":      result.Allowed,
		"robots_found": result.RobotsFound,
		"error":        errString(err),
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	return out
}

func getRobotsRespectSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if _, err := auth.GetUserIDFromRequest(r); err != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	cfg := config.GetConfig()

	writeJSON(w, http.StatusOK, map[string]bool{
		"respect_robots_txt": cfg.Scraper.RespectRobots,
	})
}
