package database

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"magpie/internal/config"
	"magpie/internal/domain"
	"magpie/internal/support"

	"sync/atomic"

	"github.com/charmbracelet/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	DB *gorm.DB
)

type Config struct {
	ExistingDB   *gorm.DB
	Dialector    gorm.Dialector
	Logger       logger.Interface
	AutoMigrate  bool
	Migrations   []any
	SeedDefaults bool
}

type Option func(*Config)

var currentDSN atomic.Value

func setDSN(dsn string) {
	if dsn == "" {
		return
	}
	currentDSN.Store(dsn)
}

func getDSN() string {
	if raw := currentDSN.Load(); raw != nil {
		if dsn, ok := raw.(string); ok {
			return dsn
		}
	}
	return ""
}

func SetupDB(opts ...Option) (*gorm.DB, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	switch {
	case cfg.ExistingDB != nil:
		DB = cfg.ExistingDB
	case cfg.Dialector != nil:
		if dsn := buildDSN(); dsn != "" {
			setDSN(dsn)
		}
		gormCfg := &gorm.Config{}
		if cfg.Logger != nil {
			gormCfg.Logger = cfg.Logger
		}
		db, err := gorm.Open(cfg.Dialector, gormCfg)
		if err != nil {
			return nil, fmt.Errorf("database: open connection: %w", err)
		}
		DB = db
		configureConnectionPool(db)
	default:
		return nil, fmt.Errorf("database: no dialector or existing connection provided")
	}

	if DB == nil {
		return nil, fmt.Errorf("database: connection was not configured")
	}

	migrations := cfg.Migrations
	if cfg.AutoMigrate && shouldSkipProxyStatisticsAutoMigrate(DB) {
		migrations = removeMigrationModel(cfg.Migrations, domain.ProxyStatistic{})
		log.Warn("Database migration: skipping ProxyStatistic auto-migrate because proxy_statistics is partitioned")
	}

	if cfg.AutoMigrate && len(migrations) > 0 {
		if err := DB.AutoMigrate(migrations...); err != nil {
			return nil, fmt.Errorf("database: auto migrate: %w", err)
		}
		log.Info("Database migration completed.")
	}

	if cfg.SeedDefaults {
		if err := seedDefaults(DB); err != nil {
			return nil, fmt.Errorf("database: seed defaults: %w", err)
		}
	}

	if cfg.AutoMigrate {
		if err := ensurePostMigrateSchemas(DB); err != nil {
			return nil, err
		}
	}

	return DB, nil
}

func defaultConfig() Config {
	dsn := buildDSN()

	setDSN(dsn)

	return Config{
		Dialector:    postgres.Open(dsn),
		Logger:       silentLogger(),
		AutoMigrate:  support.GetEnvBool("DB_AUTO_MIGRATE", !config.InProductionMode),
		Migrations:   defaultMigrations(),
		SeedDefaults: true,
	}
}

func buildDSN() string {
	dbHost := support.GetEnv("DB_HOST", "localhost")
	dbPort := support.GetEnv("DB_PORT", "5434")
	dbName := support.GetEnv("DB_NAME", "magpie")
	dbUser := support.GetEnv("DB_USERNAME", "magpie_user")
	// Default to empty instead of a shared hardcoded secret.
	dbPassword := support.GetEnv("DB_PASSWORD", "")
	dbSSLMode := resolveDBSSLMode()

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		dbHost,
		dbPort,
		dbUser,
		dbPassword,
		dbName,
		dbSSLMode,
	)

	return dsn
}

func resolveDBSSLMode() string {
	raw := strings.ToLower(strings.TrimSpace(support.GetEnv("DB_SSLMODE", "require")))

	switch raw {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return raw
	default:
		log.Warn("invalid DB_SSLMODE value, defaulting to secure mode", "value", raw, "default", "require")
		return "require"
	}
}

func silentLogger() logger.Interface {
	return logger.New(
		log.Default(),
		logger.Config{LogLevel: logger.Silent},
	)
}

func defaultMigrations() []any {
	return []any{
		domain.User{},
		domain.PasswordResetToken{},
		domain.EmailOutbox{},
		domain.Proxy{},
		domain.BlacklistedIP{},
		domain.BlacklistedRange{},
		domain.ProxyReputation{},
		domain.UserProxy{},
		domain.ProxyDailyCheck{},
		domain.ProxyDailyCheckProxyBackfill{},
		domain.RotatingProxy{},
		domain.ProxyHistory{},
		domain.ProxySnapshot{},
		domain.ProxyStatistic{},
		domain.ProxyLatestStatistic{},
		domain.ProxyOverallStatus{},
		domain.AnonymityLevel{},
		domain.Judge{},
		domain.UserJudge{},
		domain.ScrapeSite{},
		domain.UserScrapeSite{},
		domain.ProxyScrapeSite{},
		domain.Protocol{},
	}
}

func configureConnectionPool(db *gorm.DB) {
	if db == nil {
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Error("database: get sql.DB", "error", err)
		return
	}

	maxOpen := support.GetEnvInt("DB_MAX_OPEN_CONNS", 32)
	maxIdle := support.GetEnvInt("DB_MAX_IDLE_CONNS", maxOpen)
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}

	connLifetimeSeconds := support.GetEnvInt("DB_CONN_MAX_LIFETIME", 300)
	connIdleSeconds := support.GetEnvInt("DB_CONN_MAX_IDLE_TIME", 60)

	if maxOpen > 0 {
		sqlDB.SetMaxOpenConns(maxOpen)
	}
	if maxIdle >= 0 {
		sqlDB.SetMaxIdleConns(maxIdle)
	}
	if connLifetimeSeconds > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(connLifetimeSeconds) * time.Second)
	}
	if connIdleSeconds > 0 {
		sqlDB.SetConnMaxIdleTime(time.Duration(connIdleSeconds) * time.Second)
	}
}

func seedDefaults(db *gorm.DB) error {
	if err := ensureAnonymityLevels(db); err != nil {
		return err
	}
	if err := ensureProtocols(db); err != nil {
		return err
	}
	return nil
}

func ensureAnonymityLevels(db *gorm.DB) error {
	if !db.Migrator().HasTable(&domain.AnonymityLevel{}) {
		return nil
	}

	var count int64
	if err := db.Model(&domain.AnonymityLevel{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	levels := []domain.AnonymityLevel{
		{Name: "elite"},
		{Name: "anonymous"},
		{Name: "transparent"},
	}

	return db.Create(&levels).Error
}

func ensureProtocols(db *gorm.DB) error {
	if !db.Migrator().HasTable(&domain.Protocol{}) {
		return nil
	}

	var count int64
	if err := db.Model(&domain.Protocol{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	protocols := []domain.Protocol{
		{Name: "http", ID: 1},
		{Name: "https", ID: 2},
		{Name: "socks4", ID: 3},
		{Name: "socks5", ID: 4},
	}

	return db.Create(&protocols).Error
}

type schemaEnsureStep struct {
	name string
	run  func(*gorm.DB) error
}

func ensurePostMigrateSchemas(db *gorm.DB) error {
	steps := []schemaEnsureStep{
		{name: "user auth schema", run: ensureUserAuthSchema},
		{name: "proxy reputation schema", run: ensureProxyReputationSchema},
		{name: "proxy statistics retention schema", run: ensureProxyStatisticsRetentionSchema},
		{name: "proxy timeline retention schema", run: ensureProxyTimelineRetentionSchema},
		{name: "proxy query index schema", run: ensureProxyQueryIndexSchema},
		{name: "rotating proxy schema", run: ensureRotatingProxySchema},
		{name: "blacklist schema", run: ensureBlacklistSchema},
	}

	return runSchemaEnsureSteps(db, steps)
}

func ensureUserAuthSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("nil database connection")
	}
	if !db.Migrator().HasTable(&domain.User{}) {
		return nil
	}

	type duplicateEmailRow struct {
		Normalized string
		Count      int64
	}

	var duplicate duplicateEmailRow
	err := db.Raw(`
		SELECT LOWER(TRIM(email)) AS normalized, COUNT(*) AS count
		FROM users
		GROUP BY LOWER(TRIM(email))
		HAVING COUNT(*) > 1
		LIMIT 1
	`).Scan(&duplicate).Error
	if err != nil {
		return fmt.Errorf("detect duplicate canonical emails: %w", err)
	}
	if strings.TrimSpace(duplicate.Normalized) != "" {
		return fmt.Errorf("duplicate user emails exist after normalization: %s", duplicate.Normalized)
	}

	if err := db.Exec(`UPDATE users SET email = LOWER(TRIM(email)) WHERE email <> LOWER(TRIM(email)) OR email <> TRIM(email)`).Error; err != nil {
		return fmt.Errorf("normalize stored user emails: %w", err)
	}

	stmts := []string{
		`DROP INDEX IF EXISTS idx_users_email`,
		`DROP INDEX IF EXISTS users_email_key`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower_unique ON users (LOWER(email))`,
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("user auth schema: %w", err)
		}
	}

	return nil
}

func runSchemaEnsureSteps(db *gorm.DB, steps []schemaEnsureStep) error {
	for _, step := range steps {
		if err := step.run(db); err != nil {
			return fmt.Errorf("database: ensure %s: %w", step.name, err)
		}
	}
	return nil
}

func ensureBlacklistSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("nil database connection")
	}
	if !isPostgresDialect(db) {
		return nil
	}

	stmts := []string{
		`ALTER TABLE IF EXISTS blacklisted_ips ALTER COLUMN ip TYPE inet USING ip::inet`,
		`ALTER TABLE IF EXISTS blacklisted_ips DROP COLUMN IF EXISTS first_seen_at`,
		`ALTER TABLE IF EXISTS blacklisted_ips DROP COLUMN IF EXISTS last_seen_at`,
		`ALTER TABLE IF EXISTS blacklisted_ips SET UNLOGGED`,
		`CREATE INDEX IF NOT EXISTS idx_blacklisted_ips_gist ON blacklisted_ips USING gist (ip inet_ops)`,
		`ALTER TABLE IF EXISTS blacklisted_ranges ADD COLUMN IF NOT EXISTS cidr cidr`,
		`ALTER TABLE IF EXISTS blacklisted_ranges ALTER COLUMN cidr DROP NOT NULL`,
		`DO $$ BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'blacklisted_ranges' AND column_name = 'c_i_d_r'
			) THEN
				UPDATE blacklisted_ranges SET cidr = COALESCE(cidr, c_i_d_r::cidr);
				ALTER TABLE blacklisted_ranges DROP COLUMN IF EXISTS c_i_d_r;
			END IF;
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'blacklisted_ranges' AND column_name = 'c_id_r'
			) THEN
				UPDATE blacklisted_ranges SET cidr = COALESCE(cidr, c_id_r::cidr);
				ALTER TABLE blacklisted_ranges DROP COLUMN IF EXISTS c_id_r;
			END IF;
		END $$;`,
		`ALTER TABLE IF EXISTS blacklisted_ranges DROP COLUMN IF EXISTS start_ip`,
		`ALTER TABLE IF EXISTS blacklisted_ranges DROP COLUMN IF EXISTS end_ip`,
		`ALTER TABLE IF EXISTS blacklisted_ranges DROP COLUMN IF EXISTS first_seen_at`,
		`ALTER TABLE IF EXISTS blacklisted_ranges DROP COLUMN IF EXISTS last_seen_at`,
		`ALTER TABLE IF EXISTS blacklisted_ranges SET UNLOGGED`,
		`DROP INDEX IF EXISTS idx_blacklisted_ranges_cidr`,
		`CREATE INDEX IF NOT EXISTS idx_blacklisted_ranges_cidr_gist ON blacklisted_ranges USING gist (cidr inet_ops)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_blacklisted_ranges_cidr_btree ON blacklisted_ranges (cidr)`,
	}

	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("blacklist schema: %w", err)
		}
	}

	return nil
}

func ensureRotatingProxySchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("nil database connection")
	}
	if !isPostgresDialect(db) {
		return nil
	}
	if !db.Migrator().HasTable(&domain.RotatingProxy{}) {
		return nil
	}

	currentInstanceID := support.GetInstanceID()
	stmts := []string{
		`ALTER TABLE IF EXISTS rotating_proxies ADD COLUMN IF NOT EXISTS instance_id varchar(191)`,
		`ALTER TABLE IF EXISTS rotating_proxies ADD COLUMN IF NOT EXISTS instance_name varchar(120)`,
		`ALTER TABLE IF EXISTS rotating_proxies ADD COLUMN IF NOT EXISTS instance_region varchar(120)`,
		`UPDATE rotating_proxies SET instance_id = ? WHERE COALESCE(instance_id, '') = ''`,
		`UPDATE rotating_proxies SET instance_name = instance_id WHERE COALESCE(instance_name, '') = ''`,
		`UPDATE rotating_proxies SET instance_region = 'Unknown' WHERE COALESCE(instance_region, '') = ''`,
		`ALTER TABLE IF EXISTS rotating_proxies ALTER COLUMN instance_id SET NOT NULL`,
		`ALTER TABLE IF EXISTS rotating_proxies ALTER COLUMN instance_name SET NOT NULL`,
		`ALTER TABLE IF EXISTS rotating_proxies ALTER COLUMN instance_region SET NOT NULL`,
		`DROP INDEX IF EXISTS idx_rotating_proxies_listen_port`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_rotating_instance_port ON rotating_proxies (instance_id, listen_port)`,
	}

	for _, stmt := range stmts {
		if strings.Contains(stmt, "?") {
			if err := db.Exec(stmt, currentInstanceID).Error; err != nil {
				return fmt.Errorf("rotating proxy schema: %w", err)
			}
			continue
		}
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("rotating proxy schema: %w", err)
		}
	}

	return nil
}

func ensureProxyStatisticsRetentionSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("nil database connection")
	}
	if !db.Migrator().HasTable(&domain.ProxyStatistic{}) {
		return nil
	}

	if err := ensureProxyStatisticsPartitionSchema(db); err != nil {
		return fmt.Errorf("proxy statistics partition schema: %w", err)
	}

	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_proxy_statistics_created_at_id ON proxy_statistics (created_at, id)`,
	}
	if db.Migrator().HasTable(&domain.ProxyLatestStatistic{}) {
		stmts = append(stmts, `CREATE INDEX IF NOT EXISTS idx_proxy_latest_statistics_statistic_id ON proxy_latest_statistics (statistic_id)`)
	}

	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("proxy statistics retention schema: %w", err)
		}
	}

	return nil
}

func ensureProxyTimelineRetentionSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("nil database connection")
	}

	stmts := make([]string, 0, 2)
	if db.Migrator().HasTable(&domain.ProxySnapshot{}) {
		stmts = append(stmts,
			`CREATE INDEX IF NOT EXISTS idx_proxy_snapshots_created_at_id ON proxy_snapshots (created_at, id)`,
			`CREATE INDEX IF NOT EXISTS idx_proxy_snapshots_user_metric_created_id ON proxy_snapshots (user_id, metric, created_at DESC, id DESC)`,
		)
	}
	if db.Migrator().HasTable(&domain.ProxyHistory{}) {
		stmts = append(stmts,
			`CREATE INDEX IF NOT EXISTS idx_proxy_histories_created_at_id ON proxy_histories (created_at, id)`,
			`CREATE INDEX IF NOT EXISTS idx_proxy_histories_user_created_id ON proxy_histories (user_id, created_at DESC, id DESC)`,
		)
	}

	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("proxy timeline retention schema: %w", err)
		}
	}

	return nil
}

func ensureProxyQueryIndexSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("nil database connection")
	}

	stmts := make([]string, 0, 2)
	if db.Migrator().HasTable(&domain.UserProxy{}) {
		stmts = append(stmts, `CREATE INDEX IF NOT EXISTS idx_user_proxies_proxy_id ON user_proxies (proxy_id)`)
	}
	if db.Migrator().HasTable(&domain.ProxyStatistic{}) {
		stmts = append(stmts, `CREATE INDEX IF NOT EXISTS idx_proxy_statistics_proxy_created_id ON proxy_statistics (proxy_id, created_at DESC, id DESC)`)
	}
	if isPostgresDialect(db) {
		if err := db.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error; err != nil {
			log.Warn("proxy query index schema: pg_trgm extension unavailable; search will use fallback indexes", "error", err)
		} else {
			if db.Migrator().HasTable(&domain.Proxy{}) {
				stmts = append(stmts,
					`CREATE INDEX IF NOT EXISTS idx_proxies_country_search_trgm ON proxies USING gin (LOWER(COALESCE(NULLIF(country, ''), 'n/a')) gin_trgm_ops)`,
					`CREATE INDEX IF NOT EXISTS idx_proxies_estimated_type_search_trgm ON proxies USING gin (LOWER(COALESCE(NULLIF(estimated_type, ''), 'n/a')) gin_trgm_ops)`,
				)
			}
			if db.Migrator().HasTable(&domain.ProxyReputation{}) {
				stmts = append(stmts,
					`CREATE INDEX IF NOT EXISTS idx_proxy_reputations_kind_search_trgm ON proxy_reputations USING gin (LOWER(COALESCE(kind, '')) gin_trgm_ops)`,
					`CREATE INDEX IF NOT EXISTS idx_proxy_reputations_label_search_trgm ON proxy_reputations USING gin (LOWER(COALESCE(label, '')) gin_trgm_ops)`,
				)
			}
		}
	}

	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("proxy query index schema: %w", err)
		}
	}

	return nil
}

func shouldSkipProxyStatisticsAutoMigrate(db *gorm.DB) bool {
	if !isPostgresDialect(db) {
		return false
	}

	partitioned, err := isProxyStatisticsPartitioned(db)
	if err != nil {
		log.Warn("Database migration: unable to detect proxy_statistics partition state", "error", err)
		return false
	}

	return partitioned
}

func removeMigrationModel(migrations []any, model any) []any {
	if len(migrations) == 0 {
		return migrations
	}

	target := reflect.TypeOf(model)
	if target == nil {
		return migrations
	}

	filtered := make([]any, 0, len(migrations))
	for _, migration := range migrations {
		if reflect.TypeOf(migration) == target {
			continue
		}
		filtered = append(filtered, migration)
	}
	return filtered
}

func isPostgresDialect(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(db.Dialector.Name()))
	return strings.Contains(name, "postgres")
}
