/*
MIT License

Copyright (c) 2026 Jonas Kaninda

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package config

// Setting keys shared by all tools.
const (
	Storage                 = "STORAGE"
	StoragePath             = "STORAGE_PATH"
	RemotePath              = "REMOTE_PATH"
	BackupName              = "BACKUP_NAME"
	CustomName              = "BACKUP_CUSTOM_NAME"
	FileName                = "FILE_NAME"
	CronExpression          = "BACKUP_CRON_EXPRESSION"
	RunOnStart              = "BACKUP_RUN_ON_START"
	RetentionDays           = "BACKUP_RETENTION_DAYS"
	RetentionCount          = "BACKUP_RETENTION_COUNT"
	RetentionAllowRoot      = "BACKUP_RETENTION_ALLOW_ROOT"
	RetentionAdoptLegacy    = "BACKUP_RETENTION_ADOPT_LEGACY"
	IndexEnabled            = "BACKUP_INDEX"
	IndexName               = "BACKUP_INDEX_NAME"
	ConfigFile              = "BACKUP_CONFIG_FILE"
	BackupTimeout           = "BACKUP_TIMEOUT"
	RestoreTimeout          = "RESTORE_TIMEOUT"
	RescueMode              = "BACKUP_RESCUE_MODE"
	TmpDir                  = "TMP_DIR"
	Compression             = "COMPRESSION"
	CompressionLevel        = "COMPRESSION_LEVEL"
	DisableCompression      = "DISABLE_COMPRESSION"
	RestoreVerifyFirst      = "RESTORE_VERIFY_FIRST"
	LogLevel                = "LOG_LEVEL"
	LogFormat               = "LOG_FORMAT"
	TimeFormat              = "TIME_FORMAT"
	ResultFile              = "RESULT_FILE"
	MetricsAddr             = "METRICS_ADDR"
	HealthFile              = "HEALTH_FILE"
	HealthMaxAge            = "HEALTH_MAX_AGE"
	S3Endpoint              = "AWS_S3_ENDPOINT"
	S3Bucket                = "AWS_S3_BUCKET_NAME"
	S3Region                = "AWS_REGION"
	S3AccessKey             = "AWS_ACCESS_KEY"
	S3SecretKey             = "AWS_SECRET_KEY"
	S3SessionToken          = "AWS_SESSION_TOKEN"
	S3Profile               = "AWS_PROFILE"
	S3DisableSSL            = "AWS_DISABLE_SSL"
	S3ForcePathStyle        = "AWS_FORCE_PATH_STYLE"
	S3StorageClass          = "S3_STORAGE_CLASS"
	S3SSE                   = "S3_SSE"
	S3SSEKMSKeyID           = "S3_SSE_KMS_KEY_ID"
	S3PartSize              = "S3_PART_SIZE"
	S3Concurrency           = "S3_UPLOAD_CONCURRENCY"
	SFTPHost                = "SFTP_HOST"
	SFTPPort                = "SFTP_PORT"
	SFTPUser                = "SFTP_USER"
	SFTPPassword            = "SFTP_PASSWORD"
	SFTPIdentityFile        = "SFTP_IDENTITY_FILE"
	SFTPIdentityPassphrase  = "SFTP_IDENTITY_PASSPHRASE"
	SFTPKnownHosts          = "SFTP_KNOWN_HOSTS"
	SFTPIgnoreHostKey       = "SFTP_INSECURE_IGNORE_HOST_KEY"
	FTPHost                 = "FTP_HOST"
	FTPPort                 = "FTP_PORT"
	FTPUser                 = "FTP_USER"
	FTPPassword             = "FTP_PASSWORD"
	FTPTLS                  = "FTP_TLS"
	AzureAccount            = "AZURE_STORAGE_ACCOUNT_NAME"
	AzureKey                = "AZURE_STORAGE_ACCOUNT_KEY"
	AzureContainer          = "AZURE_STORAGE_CONTAINER_NAME"
	AzureEndpoint           = "AZURE_STORAGE_ENDPOINT"
	AzureSAS                = "AZURE_STORAGE_SAS_TOKEN"
	GCSBucket               = "GCS_BUCKET"
	GCSCredentialsFile      = "GCS_CREDENTIALS_FILE"
	GCSCredentialsJSON      = "GCS_CREDENTIALS_JSON"
	GCSEndpoint             = "GCS_ENDPOINT"
	Encryption              = "ENCRYPTION"
	EncryptionArmor         = "ENCRYPTION_ARMOR"
	GPGPassphrase           = "GPG_PASSPHRASE"
	GPGPublicKey            = "GPG_PUBLIC_KEY"
	GPGPrivateKey           = "GPG_PRIVATE_KEY"
	GPGPrivateKeyPassphrase = "GPG_PRIVATE_KEY_PASSPHRASE"
	AgeRecipients           = "AGE_RECIPIENTS"
	AgeRecipientsFile       = "AGE_RECIPIENTS_FILE"
	AgePassphrase           = "AGE_PASSPHRASE"
	AgeIdentity             = "AGE_IDENTITY"
	AgeIdentityFile         = "AGE_IDENTITY_FILE"
	MailHost                = "MAIL_HOST"
	MailPort                = "MAIL_PORT"
	MailUsername            = "MAIL_USERNAME"
	MailPassword            = "MAIL_PASSWORD"
	MailFrom                = "MAIL_FROM"
	MailTo                  = "MAIL_TO"
	MailSkipTLS             = "MAIL_SKIP_TLS"
	MailInsecure            = "MAIL_INSECURE_SKIP_VERIFY"
	MailTLS                 = "MAIL_TLS"
	TelegramToken           = "TG_TOKEN"
	TelegramChatID          = "TG_CHAT_ID"
	PostaURL                = "POSTA_URL"
	PostaAPIKey             = "POSTA_API_KEY"
	PostaWorkspaceID        = "POSTA_WORKSPACE_ID"
	PostaFrom               = "POSTA_FROM"
	PostaTo                 = "POSTA_TO"
	PostaTemplate           = "POSTA_TEMPLATE"
	PostaTemplateError      = "POSTA_TEMPLATE_ERROR"
	PostaLanguage           = "POSTA_LANGUAGE"
	WebhookURL              = "WEBHOOK_URL"
	WebhookSecret           = "WEBHOOK_SECRET"
	SlackWebhookURL         = "SLACK_WEBHOOK_URL"
	DiscordWebhookURL       = "DISCORD_WEBHOOK_URL"
	NtfyURL                 = "NTFY_URL"
	NtfyToken               = "NTFY_TOKEN"
	NotifyOn                = "NOTIFY_ON"
	HealthcheckPingURL      = "HEALTHCHECK_PING_URL"
	BackupReference         = "BACKUP_REFERENCE"
	TemplatesDir            = "TEMPLATES_DIR"

	DBURL         = "DB_URL"
	DBHost        = "DB_HOST"
	DBPort        = "DB_PORT"
	DBName        = "DB_NAME"
	DBUser        = "DB_USER"
	DBPassword    = "DB_PASSWORD"
	DBSSLMode     = "DB_SSL_MODE"
	DBSSLCA       = "DB_SSL_CA"
	DBSSLCert     = "DB_SSL_CERT"
	DBSSLKey      = "DB_SSL_KEY"
	AllDatabases  = "BACKUP_ALL_DATABASES"
	AllInOne      = "BACKUP_ALL_IN_ONE"
	ExcludeDBs    = "BACKUP_EXCLUDE_DATABASES"
	TargetDBURL   = "TARGET_DB_URL"
	TargetDBHost  = "TARGET_DB_HOST"
	TargetDBPort  = "TARGET_DB_PORT"
	TargetDBName  = "TARGET_DB_NAME"
	TargetDBUser  = "TARGET_DB_USER"
	TargetDBPass  = "TARGET_DB_PASSWORD"
	TargetDBSSL   = "TARGET_DB_SSL_MODE"
	TargetDBSSLCA = "TARGET_DB_SSL_CA"
)

// DBUsername is the former name of DBUser.
//
// Deprecated: use DBUser.
const DBUsername = DBUser

// Groups used in docs and `config` output.
const (
	GroupGeneral   = "general"
	GroupDatabase  = "database"
	GroupStorage   = "storage"
	GroupS3        = "storage-s3"
	GroupSFTP      = "storage-sftp"
	GroupFTP       = "storage-ftp"
	GroupAzure     = "storage-azure"
	GroupGCS       = "storage-gcs"
	GroupCrypto    = "encryption"
	GroupRetention = "retention"
	GroupNotify    = "notifications"
	GroupDaemon    = "scheduling"
)

var backupCmd = []string{"backup"}

// RegisterCommon adds the settings every tool shares.
func RegisterCommon(r *Registry) {
	r.Add(
		Setting{Key: Storage, Default: "local", Enum: []string{"local", "s3", "gcs", "azure", "sftp", "ssh", "remote", "ftp"}, Flag: "storage", Short: "s", Group: GroupStorage, Usage: "Storage backend: local, s3, gcs, azure, sftp (ssh, remote), ftp"},
		Setting{Key: StoragePath, Default: "/backup", Group: GroupStorage, Usage: "Root directory of the local storage backend"},
		Setting{Key: RemotePath, Aliases: []string{"AWS_S3_PATH", "S3_PATH"}, Flag: "path", Short: "P", FlagAliases: []string{"remote-path"}, Group: GroupStorage, Usage: "Folder inside the storage backend (without file name)"},
		Setting{Key: BackupName, Flag: "name", Short: "n", Commands: []string{"backup", "prune", "list"}, Group: GroupGeneral, Usage: "Base name of the artifact; a timestamp is appended (default: the database or volume name)"},
		Setting{Key: CustomName, Flag: "custom-name", Commands: backupCmd, Group: GroupGeneral, Usage: "Fixed artifact name without timestamp (overwrites the previous one)"},
		Setting{Key: FileName, Flag: "file", Short: "f", Commands: []string{"restore", "verify", "delete"}, Group: GroupGeneral, Usage: "Backup file to restore, verify or delete"},
		Setting{Key: CronExpression, Flag: "cron-expression", Short: "e", Commands: backupCmd, Group: GroupDaemon, Usage: "Run as a daemon on this cron schedule (5 or 6 fields, @daily, @every 6h)"},
		Setting{Key: RunOnStart, Kind: Bool, Group: GroupDaemon, Usage: "In daemon mode, run one backup immediately at start"},
		Setting{Key: RescueMode, Kind: Bool, Group: GroupDaemon, Usage: "Deprecated: runs always continue with the remaining targets after a failure"},
		Setting{Key: RetentionDays, Kind: Int, Default: "0", Group: GroupRetention, Usage: "Delete backups older than N days (0 = keep)"},
		Setting{Key: RetentionCount, Kind: Int, Default: "0", Group: GroupRetention, Usage: "Keep only the newest N backups (0 = no limit)"},
		Setting{Key: RetentionAllowRoot, Kind: Bool, Group: GroupRetention, Usage: "Allow pattern-based retention at the storage root (index disabled)"},
		Setting{Key: RetentionAdoptLegacy, Kind: Bool, Group: GroupRetention, Usage: "Import pre-index backups matching this tool's naming into the index"},
		Setting{Key: IndexEnabled, Kind: Bool, Default: "true", Group: GroupRetention, Usage: "Keep the backup index file and per-backup .meta.json sidecars"},
		Setting{Key: IndexName, Default: "{tool}-metadata.json", Flag: "index-name", Group: GroupRetention, Usage: "Index file name; placeholders {tool} {engine} {name} {db}"},
		Setting{Key: ConfigFile, Flag: "config", Short: "c", Commands: []string{"backup", "list", "prune", "config"}, Group: GroupGeneral, Usage: "YAML configuration file (default /config/config.yaml if present)"},
		Setting{Key: BackupTimeout, Kind: Duration, Group: GroupGeneral, Usage: "Abort a backup after this duration (0 = none)"},
		Setting{Key: RestoreTimeout, Kind: Duration, Group: GroupGeneral, Usage: "Abort a restore after this duration (0 = none)"},
		Setting{Key: TmpDir, Aliases: []string{"BACKUP_TMP_DIR"}, Default: "/tmp/backup", Group: GroupGeneral, Usage: "Scratch directory (only for secret files and disk-based modes)"},
		Setting{Key: Compression, Default: "gzip", Enum: []string{"gzip", "zstd", "none"}, Flag: "compression", Commands: backupCmd, Group: GroupGeneral, Usage: "Compression: gzip, zstd or none"},
		Setting{Key: CompressionLevel, Kind: Int, Group: GroupGeneral, Usage: "Compression level (0 = default)"},
		Setting{Key: DisableCompression, Kind: Bool, Flag: "disable-compression", Commands: backupCmd, Group: GroupGeneral, Usage: "Same as COMPRESSION=none"},
		Setting{Key: RestoreVerifyFirst, Default: "auto", Enum: []string{"auto", "true", "false"}, Flag: "verify-first", Commands: []string{"restore"}, Group: GroupGeneral, Usage: "Verify the whole artifact before restoring (auto: on for GPG-encrypted volumes)"},
		Setting{Key: LogLevel, Default: "info", Enum: []string{"debug", "info", "warn", "error"}, Group: GroupGeneral, Usage: "Log level"},
		Setting{Key: LogFormat, Default: "text", Enum: []string{"text", "json"}, Group: GroupGeneral, Usage: "Log format"},
		Setting{Key: TimeFormat, Default: "2006-01-02 15:04:05", Group: GroupGeneral, Usage: "Time layout used in notifications"},
		Setting{Key: ResultFile, Flag: "result-file", Group: GroupGeneral, Usage: "Write the run result as JSON to this path"},
		Setting{Key: MetricsAddr, Group: GroupDaemon, Usage: "Serve Prometheus /metrics and /healthz on this address in daemon mode (e.g. :9090)"},
		Setting{Key: HealthFile, Default: "/tmp/bkup-health.json", Group: GroupDaemon, Usage: "State file read by the health command"},
		Setting{Key: HealthMaxAge, Kind: Duration, Group: GroupDaemon, Usage: "health fails when the last success is older than this (default: 2x the schedule interval)"},

		Setting{Key: S3Endpoint, Aliases: []string{"S3_ENDPOINT"}, Compat: []string{"AWS_ENDPOINT_URL_S3", "AWS_ENDPOINT_URL"}, Group: GroupS3, Usage: "S3 endpoint (empty = AWS)"},
		Setting{Key: S3Bucket, Aliases: []string{"BUCKET_NAME", "AWS_BUCKET_NAME"}, Group: GroupS3, Usage: "S3 bucket"},
		Setting{Key: S3Region, Compat: []string{"AWS_DEFAULT_REGION"}, Default: "us-east-1", Group: GroupS3, Usage: "S3 region"},
		Setting{Key: S3AccessKey, Aliases: []string{"ACCESS_KEY"}, Compat: []string{"AWS_ACCESS_KEY_ID"}, Group: GroupS3, Usage: "S3 access key (empty = default AWS credential chain)"},
		Setting{Key: S3SecretKey, Aliases: []string{"SECRET_KEY"}, Compat: []string{"AWS_SECRET_ACCESS_KEY"}, Secret: true, Group: GroupS3, Usage: "S3 secret key"},
		Setting{Key: S3SessionToken, Secret: true, Group: GroupS3, Usage: "S3 session token"},
		Setting{Key: S3Profile, Group: GroupS3, Usage: "AWS shared config profile"},
		Setting{Key: S3DisableSSL, Kind: Bool, Group: GroupS3, Usage: "Use http for the S3 endpoint"},
		Setting{Key: S3ForcePathStyle, Kind: Bool, Group: GroupS3, Usage: "Path-style S3 addressing (MinIO)"},
		Setting{Key: S3StorageClass, Group: GroupS3, Usage: "S3 storage class"},
		Setting{Key: S3SSE, Enum: []string{"aes256", "aws:kms"}, Group: GroupS3, Usage: "Server-side encryption: AES256 or aws:kms"},
		Setting{Key: S3SSEKMSKeyID, Group: GroupS3, Usage: "KMS key id for aws:kms"},
		Setting{Key: S3PartSize, Kind: Size, Default: "64MiB", Group: GroupS3, Usage: "Multipart part size; max object size is 10000 x this"},
		Setting{Key: S3Concurrency, Kind: Int, Default: "2", Group: GroupS3, Usage: "Parallel part uploads (memory = part size x this)"},

		Setting{Key: SFTPHost, Group: GroupSFTP, Usage: "SFTP host"},
		Setting{Key: SFTPPort, Kind: Int, Default: "22", Group: GroupSFTP, Usage: "SFTP port"},
		Setting{Key: SFTPUser, Group: GroupSFTP, Usage: "SFTP user"},
		Setting{Key: SFTPPassword, Secret: true, Group: GroupSFTP, Usage: "SFTP password"},
		Setting{Key: SFTPIdentityFile, Group: GroupSFTP, Usage: "SFTP private key file (the SSH agent is also used when SSH_AUTH_SOCK is set)"},
		Setting{Key: SFTPIdentityPassphrase, Secret: true, Group: GroupSFTP, Usage: "Passphrase of the SFTP private key"},
		Setting{Key: SFTPKnownHosts, Group: GroupSFTP, Usage: "known_hosts file used to verify the SFTP host key (default ~/.ssh/known_hosts)"},
		Setting{Key: SFTPIgnoreHostKey, Kind: Bool, Group: GroupSFTP, Usage: "Do not verify the SFTP host key (insecure)"},

		Setting{Key: FTPHost, Aliases: []string{"FTP_HOST_NAME"}, Group: GroupFTP, Usage: "FTP host"},
		Setting{Key: FTPPort, Kind: Int, Default: "21", Group: GroupFTP, Usage: "FTP port"},
		Setting{Key: FTPUser, Group: GroupFTP, Usage: "FTP user"},
		Setting{Key: FTPPassword, Secret: true, Group: GroupFTP, Usage: "FTP password"},
		Setting{Key: FTPTLS, Kind: Bool, Group: GroupFTP, Usage: "Use explicit FTPS"},

		Setting{Key: AzureAccount, Group: GroupAzure, Usage: "Azure storage account"},
		Setting{Key: AzureKey, Secret: true, Group: GroupAzure, Usage: "Azure storage account key"},
		Setting{Key: AzureContainer, Group: GroupAzure, Usage: "Azure blob container"},
		Setting{Key: AzureEndpoint, Group: GroupAzure, Usage: "Azure blob endpoint (Azurite, sovereign clouds)"},
		Setting{Key: AzureSAS, Secret: true, Group: GroupAzure, Usage: "Azure SAS token (instead of the account key)"},

		Setting{Key: GCSBucket, Group: GroupGCS, Usage: "GCS bucket"},
		Setting{Key: GCSCredentialsFile, Compat: []string{"GOOGLE_APPLICATION_CREDENTIALS"}, Group: GroupGCS, Usage: "GCS service account JSON file"},
		Setting{Key: GCSCredentialsJSON, Secret: true, Group: GroupGCS, Usage: "GCS service account JSON content"},
		Setting{Key: GCSEndpoint, Group: GroupGCS, Usage: "GCS endpoint (emulators)"},

		Setting{Key: Encryption, Default: "auto", Enum: []string{"auto", "none", "gpg", "age"}, Flag: "encryption", Commands: backupCmd, Group: GroupCrypto, Usage: "auto (from the keys set), none, gpg or age"},
		Setting{Key: EncryptionArmor, Kind: Bool, Group: GroupCrypto, Usage: "ASCII-armor encrypted output"},
		Setting{Key: GPGPassphrase, Secret: true, Group: GroupCrypto, Usage: "GPG symmetric passphrase"},
		Setting{Key: GPGPublicKey, Group: GroupCrypto, Usage: "GPG public key(s): file path or inline key (default /config/gnupg/public_key.{asc,gpg})"},
		Setting{Key: GPGPrivateKey, Secret: true, Group: GroupCrypto, Usage: "GPG private key for restore: file path or inline key (default /config/gnupg/private_key.{asc,gpg})"},
		Setting{Key: GPGPrivateKeyPassphrase, Secret: true, Group: GroupCrypto, Usage: "Passphrase of the GPG private key (default GPG_PASSPHRASE)"},
		Setting{Key: AgeRecipients, Group: GroupCrypto, Usage: "age recipients (age1..., ssh-ed25519 ...), comma or newline separated"},
		Setting{Key: AgeRecipientsFile, Group: GroupCrypto, Usage: "File with age recipients"},
		Setting{Key: AgePassphrase, Secret: true, Group: GroupCrypto, Usage: "age passphrase (scrypt)"},
		Setting{Key: AgeIdentity, Secret: true, Group: GroupCrypto, Usage: "age identity (AGE-SECRET-KEY-...) for restore"},
		Setting{Key: AgeIdentityFile, Group: GroupCrypto, Usage: "age identity file for restore (default /config/age/identity.txt)"},

		Setting{Key: MailHost, Group: GroupNotify, Usage: "SMTP host"},
		Setting{Key: MailPort, Kind: Int, Default: "587", Group: GroupNotify, Usage: "SMTP port"},
		Setting{Key: MailUsername, Group: GroupNotify, Usage: "SMTP user"},
		Setting{Key: MailPassword, Secret: true, Group: GroupNotify, Usage: "SMTP password"},
		Setting{Key: MailFrom, Group: GroupNotify, Usage: "Sender address"},
		Setting{Key: MailTo, Group: GroupNotify, Usage: "Recipients, comma separated"},
		Setting{Key: MailSkipTLS, Kind: Bool, Group: GroupNotify, Usage: "Deprecated: true skips certificate verification (use MAIL_INSECURE_SKIP_VERIFY)"},
		Setting{Key: MailInsecure, Kind: Bool, Group: GroupNotify, Usage: "Skip SMTP certificate verification"},
		Setting{Key: MailTLS, Default: "starttls", Enum: []string{"starttls", "tls", "none"}, Group: GroupNotify, Usage: "SMTP TLS mode"},
		Setting{Key: TelegramToken, Compat: []string{"TELEGRAM_BOT_TOKEN"}, Secret: true, Group: GroupNotify, Usage: "Telegram bot token"},
		Setting{Key: TelegramChatID, Compat: []string{"TELEGRAM_CHAT_ID"}, Group: GroupNotify, Usage: "Telegram chat id"},
		Setting{Key: PostaURL, Group: GroupNotify, Usage: "Posta base URL (https://posta.example.com) for email notifications through Posta"},
		Setting{Key: PostaAPIKey, Secret: true, Group: GroupNotify, Usage: "Posta API key with the send scope"},
		Setting{Key: PostaWorkspaceID, Kind: Int, Group: GroupNotify, Usage: "Posta workspace id (only for account-wide API keys)"},
		Setting{Key: PostaFrom, Group: GroupNotify, Usage: "Posta sender address (default MAIL_FROM)"},
		Setting{Key: PostaTo, Group: GroupNotify, Usage: "Posta recipients, comma separated (default MAIL_TO)"},
		Setting{Key: PostaTemplate, Group: GroupNotify, Usage: "Posta template name for notifications; the event fields are passed as template data (default: built-in HTML)"},
		Setting{Key: PostaTemplateError, Group: GroupNotify, Usage: "Posta template name for failures (default POSTA_TEMPLATE)"},
		Setting{Key: PostaLanguage, Group: GroupNotify, Usage: "Posta template language"},
		Setting{Key: WebhookURL, Group: GroupNotify, Usage: "Generic JSON webhook URL"},
		Setting{Key: WebhookSecret, Secret: true, Group: GroupNotify, Usage: "HMAC-SHA256 secret for the X-Bkup-Signature header"},
		Setting{Key: SlackWebhookURL, Secret: true, Group: GroupNotify, Usage: "Slack incoming webhook URL"},
		Setting{Key: DiscordWebhookURL, Secret: true, Group: GroupNotify, Usage: "Discord webhook URL"},
		Setting{Key: NtfyURL, Group: GroupNotify, Usage: "ntfy topic URL"},
		Setting{Key: NtfyToken, Secret: true, Group: GroupNotify, Usage: "ntfy access token"},
		Setting{Key: NotifyOn, Default: "always", Enum: []string{"always", "failure", "success", "never"}, Group: GroupNotify, Usage: "When to notify"},
		Setting{Key: HealthcheckPingURL, Group: GroupNotify, Usage: "healthchecks.io-style URL pinged on start (/start), success and failure (/fail)"},
		Setting{Key: BackupReference, Group: GroupNotify, Usage: "Free text included in notifications"},
		Setting{Key: TemplatesDir, Default: "/config/templates", Group: GroupNotify, Usage: "Directory with custom notification templates"},
	)
}

// RegisterDatabase adds the connection settings shared by database tools.
func RegisterDatabase(r *Registry, defaultPort string) {
	r.Add(
		Setting{Key: DBURL, Secret: true, Group: GroupDatabase, Usage: "Connection URL (overrides host, port, user, password)"},
		Setting{Key: DBHost, Default: "localhost", Group: GroupDatabase, Usage: "Database host"},
		Setting{Key: DBPort, Kind: Int, Default: defaultPort, Group: GroupDatabase, Usage: "Database port"},
		Setting{Key: DBName, Flag: "dbname", Short: "d", Group: GroupDatabase, Usage: "Database name"},
		Setting{Key: DBUser, Compat: []string{"DB_USERNAME"}, Group: GroupDatabase, Usage: "Database user"},
		Setting{Key: DBPassword, Secret: true, Group: GroupDatabase, Usage: "Database password"},
		Setting{Key: DBSSLMode, Group: GroupDatabase, Usage: "TLS mode"},
		Setting{Key: DBSSLCA, Group: GroupDatabase, Usage: "CA certificate file"},
		Setting{Key: DBSSLCert, Group: GroupDatabase, Usage: "Client certificate file"},
		Setting{Key: DBSSLKey, Group: GroupDatabase, Usage: "Client key file"},
		Setting{Key: AllDatabases, Kind: Bool, Flag: "all-databases", Short: "a", Commands: []string{"backup", "migrate"}, Group: GroupDatabase, Usage: "Back up every database, one file each"},
		Setting{Key: AllInOne, Kind: Bool, Flag: "all-in-one", Short: "A", Commands: backupCmd, Group: GroupDatabase, Usage: "Back up every database into a single file"},
		Setting{Key: ExcludeDBs, Flag: "exclude-db", Commands: backupCmd, Group: GroupDatabase, Usage: "Databases to skip with --all-databases, comma separated"},
		Setting{Key: TargetDBURL, Secret: true, Group: GroupDatabase, Usage: "migrate: target connection URL"},
		Setting{Key: TargetDBHost, Group: GroupDatabase, Usage: "migrate: target host"},
		Setting{Key: TargetDBPort, Kind: Int, Default: defaultPort, Group: GroupDatabase, Usage: "migrate: target port"},
		Setting{Key: TargetDBName, Group: GroupDatabase, Usage: "migrate: target database"},
		Setting{Key: TargetDBUser, Compat: []string{"TARGET_DB_USERNAME"}, Group: GroupDatabase, Usage: "migrate: target user"},
		Setting{Key: TargetDBPass, Secret: true, Group: GroupDatabase, Usage: "migrate: target password"},
		Setting{Key: TargetDBSSL, Group: GroupDatabase, Usage: "migrate: target TLS mode"},
		Setting{Key: TargetDBSSLCA, Group: GroupDatabase, Usage: "migrate: target CA certificate file"},
	)
}
