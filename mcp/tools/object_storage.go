package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Pantech Object Storage provides S3-compatible buckets, objects, lifecycle
// and CORS rules, object lock, cross-organization shares, event notifications
// and custom domains. Access keys are created in the console or CLI so that a
// secret access key never enters an AI app's context; this server only lists
// and revokes keys. Customer-chosen bucket names, object keys, tags, prefixes
// and hostnames stay in fenced data, never in summaries.

type objectStorageStatus struct {
	Enabled             bool                         `json:"enabled"`
	Endpoint            string                       `json:"endpoint"`
	Beta                bool                         `json:"beta"`
	DirectUploadEnabled bool                         `json:"direct_upload_enabled"`
	BlockPublicAccess   *bool                        `json:"block_public_access,omitempty"`
	Limits              *pantech.ObjectStorageLimits `json:"limits"`
}

type objectStorageSettings struct {
	BlockPublicAccess bool `json:"block_public_access"`
}

// objectStorageKey deliberately omits secret_access_key so a secret can never
// be decoded or returned.
type objectStorageKey struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Region      string   `json:"region"`
	AccessKeyID string   `json:"access_key_id"`
	ReadOnly    bool     `json:"read_only"`
	Permissions string   `json:"permissions,omitempty"`
	Buckets     []string `json:"buckets"`
	Prefixes    []string `json:"prefixes,omitempty"`
	CreatedAt   string   `json:"created_at"`
	ExpiresAt   *string  `json:"expires_at,omitempty"`
}

type objectStorageBucket struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Region         string   `json:"region"`
	Endpoint       string   `json:"endpoint"`
	QuotaBytes     int64    `json:"quota_bytes"`
	SizeBytes      int64    `json:"size_bytes"`
	ObjectCount    int64    `json:"object_count"`
	Versioning     bool     `json:"versioning"`
	ObjectLock     bool     `json:"object_lock"`
	Encryption     string   `json:"encryption,omitempty"`
	PublicPrefixes []string `json:"public_prefixes"`
	State          string   `json:"state"`
	CreatedAt      string   `json:"created_at"`
	RetiredAt      *string  `json:"retired_at,omitempty"`
	PurgeDueAt     *string  `json:"purge_due_at,omitempty"`
}

type createdObjectStorageBucket struct {
	objectStorageBucket
	OperationID string `json:"operation_id"`
}

type updatedObjectStorageBucket struct {
	objectStorageBucket
	OperationID *string `json:"operation_id"`
}

type bucketObjectMeta struct {
	Key                  string               `json:"key"`
	SizeBytes            int64                `json:"size_bytes"`
	ETag                 string               `json:"etag"`
	ContentType          string               `json:"content_type"`
	CacheControl         string               `json:"cache_control"`
	ContentDisposition   string               `json:"content_disposition"`
	LastModified         *string              `json:"last_modified"`
	StorageClass         string               `json:"storage_class"`
	VersionID            string               `json:"version_id"`
	Metadata             map[string]string    `json:"metadata"`
	Tags                 []pantech.StorageTag `json:"tags"`
	RetentionMode        string               `json:"retention_mode"`
	RetainUntil          *string              `json:"retain_until"`
	LegalHold            bool                 `json:"legal_hold"`
	Public               bool                 `json:"public"`
	ObjectPublic         bool                 `json:"object_public"`
	PublicSource         string               `json:"public_source"`
	PublicURL            string               `json:"public_url"`
	ServerSideEncryption string               `json:"server_side_encryption,omitempty"`
}

type restoreBucketObjectVersionResult struct {
	Key               string `json:"key"`
	Action            string `json:"action"`
	RestoredVersionID string `json:"restored_version_id"`
	VersionID         string `json:"version_id"`
	ETag              string `json:"etag"`
	LastModified      string `json:"last_modified"`
}

var bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func checkBucketName(name string) error {
	if !bucketNamePattern.MatchString(name) {
		return errorf("name must be 3 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, e.g. assets.")
	}
	return nil
}

func checkBucketEncryption(enc string) error {
	if enc != "" && enc != "none" && enc != "sse_s3" {
		return errorf("encryption must be none or sse_s3.")
	}
	return nil
}

func checkUsageRange(r string) error {
	if r != "" && r != "24h" && r != "7d" && r != "30d" {
		return errorf("range must be 24h, 7d or 30d.")
	}
	return nil
}

func checkSharePermissions(p string) error {
	if p != "read_only" && p != "write_only" && p != "read_write" {
		return errorf("permissions must be read_only, write_only or read_write.")
	}
	return nil
}

func checkNotificationEvents(events []string) error {
	if len(events) == 0 {
		return errorf("events must list at least one event: object.created or object.removed.")
	}
	for _, ev := range events {
		if ev != "object.created" && ev != "object.removed" {
			return errorf("each event must be object.created or object.removed.")
		}
	}
	return nil
}

func checkNotificationTargetType(tt string) error {
	if tt != "webhook" && tt != "function" {
		return errorf("target_type must be webhook or function.")
	}
	return nil
}

func checkCORSRules(rules []bucketCORSRuleInput) error {
	allowed := []string{"GET", "PUT", "POST", "DELETE", "HEAD"}
	for i, r := range rules {
		if len(r.AllowedOrigins) == 0 {
			return errorf("rules[%d].allowed_origins must list at least one origin.", i)
		}
		if len(r.AllowedMethods) == 0 {
			return errorf("rules[%d].allowed_methods must list at least one HTTP method.", i)
		}
		for _, m := range r.AllowedMethods {
			if !slices.Contains(allowed, m) {
				return errorf("rules[%d].allowed_methods must use GET, PUT, POST, DELETE or HEAD.", i)
			}
		}
		if r.MaxAgeSeconds < 0 {
			return errorf("rules[%d].max_age_seconds must be 0 or greater.", i)
		}
	}
	return nil
}

func checkLifecycleRules(rules []bucketLifecycleRuleInput) error {
	for i, r := range rules {
		if r.ExpirationDays < 0 || r.NoncurrentExpirationDays < 0 || r.AbortIncompleteMultipartDays < 0 {
			return errorf("rules[%d] day values must be 0 or greater.", i)
		}
	}
	return nil
}

func checkObjectLock(enabled bool, mode string, days, years int) error {
	if days < 0 || years < 0 {
		return errorf("days and years must be 0 or greater.")
	}
	if !enabled {
		return nil
	}
	if mode != "GOVERNANCE" && mode != "COMPLIANCE" {
		return errorf("mode must be GOVERNANCE or COMPLIANCE when enabled is true.")
	}
	if days == 0 && years == 0 {
		return errorf("set days or years to at least 1 when enabled is true.")
	}
	if days > 0 && years > 0 {
		return errorf("set either days or years, not both.")
	}
	return nil
}

func bucketPath(id string) string { return "/buckets/" + id }

func bucketText(b *objectStorageBucket) string {
	text := fmt.Sprintf("Bucket %s in %s is %s: %s, %s.",
		arg(b.ID), arg(b.Region), arg(b.State), plural(int(b.ObjectCount), "object", "objects"), gib(b.SizeBytes))
	if b.QuotaBytes > 0 {
		text += fmt.Sprintf(" Quota %s.", gib(b.QuotaBytes))
	}
	if b.Versioning {
		text += " Versioning is on."
	}
	if b.ObjectLock {
		text += " Object lock is on."
	}
	if b.Encryption == "sse_s3" {
		text += " Default encryption is sse_s3."
	}
	if len(b.PublicPrefixes) > 0 {
		text += fmt.Sprintf(" %s publicly readable.", plural(len(b.PublicPrefixes), "prefix is", "prefixes are"))
	} else {
		text += " Wholly private."
	}
	return text
}

type objectStorageUsageInput struct {
	Range string `json:"range,omitempty" jsonschema:"Time window: 24h, 7d or 30d. Defaults to 24h."`
}

type listObjectStorageKeysInput struct {
	Region string `json:"region,omitempty" jsonschema:"Region code, e.g. af-abj. Leave out for the default object storage region."`
}

type updateObjectStorageSettingsInput struct {
	BlockPublicAccess bool `json:"block_public_access" jsonschema:"true: block all public access across the organization (public prefixes and public objects stop being served anonymously). false: restore configured public prefixes and objects."`
	Confirmation
}

type revokeObjectStorageKeyInput struct {
	StorageKeyID string `json:"storage_key_id" jsonschema:"The access key's id, e.g. osk_…, from list_object_storage_keys."`
	Confirmation
}

type bucketInput struct {
	BucketID string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…, from list_buckets."`
}

type createBucketInput struct {
	Name           string   `json:"name" jsonschema:"3 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, e.g. assets."`
	Region         string   `json:"region,omitempty" jsonschema:"Region code, e.g. af-abj. Defaults to the region object storage is offered in."`
	QuotaBytes     *int64   `json:"quota_bytes,omitempty" jsonschema:"Optional limit on stored bytes; 0 or leave out for none."`
	Versioning     *bool    `json:"versioning,omitempty" jsonschema:"Keep earlier versions of overwritten and deleted objects. Versions count as stored data."`
	ObjectLock     *bool    `json:"object_lock,omitempty" jsonschema:"Allow objects to be locked against deletion. Can only be enabled at create and turns versioning on."`
	Encryption     string   `json:"encryption,omitempty" jsonschema:"Default server-side encryption for new objects: none or sse_s3."`
	PublicPrefixes []string `json:"public_prefixes,omitempty" jsonschema:"Key prefixes anyone on the internet may read without a key, e.g. public/. At most 20."`
	Confirmation
}

type updateBucketInput struct {
	BucketID       string    `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	QuotaBytes     *int64    `json:"quota_bytes,omitempty" jsonschema:"New limit on stored bytes; 0 removes the quota."`
	Versioning     *bool     `json:"versioning,omitempty" jsonschema:"true to keep earlier versions of overwritten and deleted objects; false to stop keeping new versions."`
	Encryption     string    `json:"encryption,omitempty" jsonschema:"Default server-side encryption for newly written objects: none or sse_s3."`
	PublicPrefixes *[]string `json:"public_prefixes,omitempty" jsonschema:"Replaces the bucket's public prefixes; an empty list makes the bucket wholly private."`
	Confirmation
}

type emptyBucketInput struct {
	BucketID string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Confirmation
}

type deleteBucketInput struct {
	BucketID string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Force    bool   `json:"force,omitempty" jsonschema:"true to delete the bucket even when it still holds objects (they are deleted with it)."`
	Confirmation
}

type listBucketObjectsInput struct {
	BucketID        string  `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Prefix          string  `json:"prefix,omitempty" jsonschema:"Only keys starting with this prefix, e.g. images/."`
	Delimiter       *string `json:"delimiter,omitempty" jsonschema:"Folder separator, e.g. / to group keys into folders, or empty string for a flat listing."`
	Q               string  `json:"q,omitempty" jsonschema:"Search query across keys in the bucket."`
	Cursor          string  `json:"cursor,omitempty" jsonschema:"Pagination cursor from next_cursor of the previous call."`
	Limit           int     `json:"limit,omitempty" jsonschema:"Maximum entries per page (1 to 1000)."`
	Versions        bool    `json:"versions,omitempty" jsonschema:"true to list object versions and delete markers instead of current objects."`
	KeyMarker       string  `json:"key_marker,omitempty" jsonschema:"Key marker for versioned pagination (next_key_marker)."`
	VersionIDMarker string  `json:"version_id_marker,omitempty" jsonschema:"Version id marker for versioned pagination (next_version_id_marker)."`
}

type bucketObjectMetaInput struct {
	BucketID  string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Key       string `json:"key" jsonschema:"The object's key, e.g. images/logo.png."`
	VersionID string `json:"version_id,omitempty" jsonschema:"Optional version id for a versioned bucket."`
}

type setBucketObjectVisibilityInput struct {
	BucketID  string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Key       string `json:"key" jsonschema:"The object's key."`
	VersionID string `json:"version_id,omitempty" jsonschema:"Optional version id."`
	Public    bool   `json:"public" jsonschema:"true: anyone on the internet can read this object without a key. false: make the object private (unless covered by a bucket public prefix)."`
	Confirmation
}

type restoreBucketObjectVersionInput struct {
	BucketID  string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Key       string `json:"key" jsonschema:"The object's key."`
	VersionID string `json:"version_id,omitempty" jsonschema:"Version id to restore as the latest version, or the delete marker's version id to undelete."`
}

type deleteBucketObjectItemInput struct {
	Key       string `json:"key" jsonschema:"The object's key."`
	VersionID string `json:"version_id,omitempty" jsonschema:"Optional version id to permanently delete that specific version."`
}

type deleteBucketObjectsInput struct {
	BucketID        string                        `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Items           []deleteBucketObjectItemInput `json:"items,omitempty" jsonschema:"Specific objects or versions to delete."`
	RecursivePrefix string                        `json:"recursive_prefix,omitempty" jsonschema:"Delete every object under this folder prefix, e.g. temp/."`
	Confirmation
}

type bucketCORSRuleInput struct {
	ID             string   `json:"id,omitempty" jsonschema:"Optional rule identifier."`
	AllowedOrigins []string `json:"allowed_origins" jsonschema:"Origins allowed to make cross-origin requests, e.g. https://example.com or *."`
	AllowedMethods []string `json:"allowed_methods" jsonschema:"HTTP methods allowed: GET, PUT, POST, DELETE, HEAD."`
	AllowedHeaders []string `json:"allowed_headers,omitempty" jsonschema:"Request headers allowed in preflight requests."`
	ExposeHeaders  []string `json:"expose_headers,omitempty" jsonschema:"Response headers exposed to browsers."`
	MaxAgeSeconds  int      `json:"max_age_seconds,omitempty" jsonschema:"Preflight cache duration in seconds (0 or greater)."`
}

type setBucketCORSInput struct {
	BucketID string                `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Rules    []bucketCORSRuleInput `json:"rules" jsonschema:"The complete new list of CORS rules; pass an empty list to clear all rules."`
	Confirmation
}

type bucketLifecycleRuleInput struct {
	ID                           string `json:"id,omitempty" jsonschema:"Optional rule identifier."`
	Prefix                       string `json:"prefix,omitempty" jsonschema:"Key prefix the rule applies to; empty applies to the whole bucket."`
	Enabled                      bool   `json:"enabled" jsonschema:"Whether the rule is active."`
	ExpirationDays               int    `json:"expiration_days,omitempty" jsonschema:"Days after creation to expire current objects (0 for none)."`
	NoncurrentExpirationDays     int    `json:"noncurrent_expiration_days,omitempty" jsonschema:"Days after becoming noncurrent to delete older versions (0 for none)."`
	AbortIncompleteMultipartDays int    `json:"abort_incomplete_multipart_days,omitempty" jsonschema:"Days after initiation to abort incomplete multipart uploads (0 for none)."`
	ExpiredObjectDeleteMarker    bool   `json:"expired_object_delete_marker,omitempty" jsonschema:"true to remove expired delete markers with no noncurrent versions."`
}

type setBucketLifecycleInput struct {
	BucketID string                     `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Rules    []bucketLifecycleRuleInput `json:"rules" jsonschema:"The complete new list of lifecycle rules; pass an empty list to clear all rules."`
	Confirmation
}

type bucketTagInput struct {
	Key   string `json:"key" jsonschema:"Tag key."`
	Value string `json:"value" jsonschema:"Tag value."`
}

type setBucketTagsInput struct {
	BucketID string           `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Tags     []bucketTagInput `json:"tags" jsonschema:"The complete new list of key-value tags; pass an empty list to clear all tags."`
	Confirmation
}

type setBucketObjectLockInput struct {
	BucketID string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Enabled  bool   `json:"enabled" jsonschema:"Whether default retention is enabled for newly uploaded objects."`
	Mode     string `json:"mode,omitempty" jsonschema:"Retention mode when enabled: GOVERNANCE or COMPLIANCE (COMPLIANCE cannot be shortened or deleted by anyone until expiry)."`
	Days     int    `json:"days,omitempty" jsonschema:"Retention period in days (set days or years, not both)."`
	Years    int    `json:"years,omitempty" jsonschema:"Retention period in years (set days or years, not both)."`
	Confirmation
}

type createBucketShareInput struct {
	BucketID              string   `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	GranteeOrganizationID string   `json:"grantee_organization_id" jsonschema:"The Pantech organization id receiving access, e.g. org_…"`
	Permissions           string   `json:"permissions" jsonschema:"Access granted: read_only, write_only or read_write."`
	Prefixes              []string `json:"prefixes,omitempty" jsonschema:"Optional key prefixes limiting the share; leave out for the whole bucket."`
	ExpiresAt             string   `json:"expires_at,omitempty" jsonschema:"Optional RFC 3339 expiry time, e.g. 2027-01-01T00:00:00Z."`
	Confirmation
}

type revokeBucketShareInput struct {
	BucketID string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	ShareID  string `json:"share_id" jsonschema:"The share's id from list_bucket_shares."`
	Confirmation
}

type createBucketNotificationInput struct {
	BucketID   string   `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Name       string   `json:"name" jsonschema:"A descriptive name for the notification rule."`
	Events     []string `json:"events" jsonschema:"Events to subscribe to: object.created and/or object.removed."`
	Prefix     string   `json:"prefix,omitempty" jsonschema:"Optional key prefix filter, e.g. uploads/."`
	Suffix     string   `json:"suffix,omitempty" jsonschema:"Optional key suffix filter, e.g. .jpg."`
	TargetType string   `json:"target_type" jsonschema:"Target type: webhook or function."`
	TargetID   string   `json:"target_id" jsonschema:"The webhook or function id receiving notifications."`
	Enabled    *bool    `json:"enabled,omitempty" jsonschema:"Whether the rule is active (default true)."`
}

type updateBucketNotificationInput struct {
	BucketID       string   `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	NotificationID string   `json:"notification_id" jsonschema:"The notification rule's id from list_bucket_notifications."`
	Events         []string `json:"events,omitempty" jsonschema:"New event list: object.created and/or object.removed."`
	Prefix         *string  `json:"prefix,omitempty" jsonschema:"New key prefix filter."`
	Suffix         *string  `json:"suffix,omitempty" jsonschema:"New key suffix filter."`
	Enabled        *bool    `json:"enabled,omitempty" jsonschema:"Enable or disable the rule."`
	Confirmation
}

type deleteBucketNotificationInput struct {
	BucketID       string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	NotificationID string `json:"notification_id" jsonschema:"The notification rule's id from list_bucket_notifications."`
	Confirmation
}

type createBucketDomainInput struct {
	BucketID        string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	Hostname        string `json:"hostname" jsonschema:"Custom domain hostname, e.g. assets.example.com."`
	CertificateID   string `json:"certificate_id" jsonschema:"Certificate Manager certificate id covering hostname, e.g. cert_…"`
	IndexDocument   string `json:"index_document,omitempty" jsonschema:"Optional index document for directory paths, e.g. index.html."`
	ErrorDocument   string `json:"error_document,omitempty" jsonschema:"Optional error document, e.g. 404.html."`
	SPAMode         *bool  `json:"spa_mode,omitempty" jsonschema:"true to serve index_document for missing routes (single-page apps)."`
	CacheTTLSeconds *int   `json:"cache_ttl_seconds,omitempty" jsonschema:"Cache TTL in seconds (0 or greater)."`
	Confirmation
}

type updateBucketDomainInput struct {
	BucketID        string  `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	DomainID        string  `json:"domain_id" jsonschema:"The custom domain binding's id from list_bucket_domains."`
	CertificateID   *string `json:"certificate_id,omitempty" jsonschema:"New Certificate Manager certificate id."`
	IndexDocument   *string `json:"index_document,omitempty" jsonschema:"New index document."`
	ErrorDocument   *string `json:"error_document,omitempty" jsonschema:"New error document."`
	SPAMode         *bool   `json:"spa_mode,omitempty" jsonschema:"Enable or disable single-page app fallback routing."`
	CacheTTLSeconds *int    `json:"cache_ttl_seconds,omitempty" jsonschema:"New cache TTL in seconds (0 or greater)."`
	Confirmation
}

type deleteBucketDomainInput struct {
	BucketID string `json:"bucket_id" jsonschema:"The bucket's id, e.g. bkt_…"`
	DomainID string `json:"domain_id" jsonschema:"The custom domain binding's id from list_bucket_domains."`
	Confirmation
}

func init() {
	// --- Read tools ---

	add(Spec{
		Name: "get_object_storage", Title: "Get object storage status",
		Description: "Get whether object storage is enabled for the organization, its S3 endpoint, whether Block All Public Access is on, direct upload availability and storage limits.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /object-storage"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var s objectStorageStatus
		if err := c.Client.Do(ctx, "GET", "/object-storage", nil, nil, &s); err != nil {
			return nil, err
		}
		if !s.Enabled {
			return ok("Object storage is not enabled for this organization yet.", s)
		}
		text := fmt.Sprintf("Object storage is enabled at %s.", arg(s.Endpoint))
		if s.BlockPublicAccess != nil {
			if *s.BlockPublicAccess {
				text += " Block All Public Access is on."
			} else {
				text += " Block All Public Access is off."
			}
		}
		if s.Limits != nil {
			text += fmt.Sprintf(" Limits: %s, %s storage, %d objects.",
				plural(s.Limits.MaxBuckets, "bucket", "buckets"), gib(s.Limits.MaxStorageBytes), s.Limits.MaxObjects)
		}
		return ok(text, s)
	})

	add(Spec{
		Name: "get_object_storage_usage", Title: "Get object storage usage",
		Description: "Get hourly object storage transfer (bytes sent and received) and request counts (read and write operations) for the organization over 24h, 7d or 30d. Exact measured quantities.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /object-storage/usage"},
	}, func(ctx context.Context, c *Call, in objectStorageUsageInput) (*Result, error) {
		if err := checkUsageRange(in.Range); err != nil {
			return nil, err
		}
		var params *pantech.ObjectStorageUsageParams
		if in.Range != "" {
			params = &pantech.ObjectStorageUsageParams{Range: in.Range}
		}
		u, err := c.Client.ObjectStorage.GetUsage(ctx, params)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Object storage usage (%s): %s sent, %s received, %d read ops, %d write ops across %s.",
			arg(u.Range), gib(u.TotalBytesSent), gib(u.TotalBytesReceived), u.TotalReadOps, u.TotalWriteOps, plural(len(u.Hours), "hour", "hours")), u)
	})

	add(Spec{
		Name: "list_object_storage_keys", Title: "List object storage access keys",
		Description: "List the organization's active S3 access keys (access key id, permissions, bucket and prefix scopes, expiry). Secret access keys are never returned; create new keys in the console or CLI.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /object-storage/keys"},
	}, func(ctx context.Context, c *Call, in listObjectStorageKeysInput) (*Result, error) {
		if err := checkOptionalIDs("region", in.Region); err != nil {
			return nil, err
		}
		q := url.Values{}
		setIf(q, "region", in.Region)
		var page pantech.Page[objectStorageKey]
		if err := c.Client.Do(ctx, "GET", "/object-storage/keys", q, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "object storage access key", "object storage access keys")
	})

	add(Spec{
		Name: "list_buckets", Title: "List object storage buckets",
		Description: "List the organization's object storage buckets, their region, S3 endpoint, size, object count, quota, versioning, object lock, encryption, public prefixes and state.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var page pantech.Page[objectStorageBucket]
		if err := c.Client.Do(ctx, "GET", "/buckets", nil, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "bucket", "buckets")
	})

	add(Spec{
		Name: "list_shared_buckets", Title: "List buckets shared with this organization",
		Description: "List object storage buckets shared with your organization by other Pantech organizations.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/shared"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var page pantech.Page[map[string]any]
		if err := c.Client.Do(ctx, "GET", "/buckets/shared", nil, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "shared bucket", "shared buckets")
	})

	add(Spec{
		Name: "get_bucket", Title: "Get an object storage bucket",
		Description: "Get one object storage bucket: its region, S3 endpoint, stored bytes, object count, quota, versioning, object lock, encryption, public prefixes and state.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		return ok(bucketText(b), b)
	})

	add(Spec{
		Name: "get_bucket_stats", Title: "Get bucket storage stats",
		Description: "Get live stored bytes and object count for a bucket.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/stats"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		s, err := c.Client.Buckets.GetStats(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Bucket %s holds %s (%s).", in.BucketID, plural(int(s.ObjectCount), "object", "objects"), gib(s.SizeBytes)), s)
	})

	add(Spec{
		Name: "list_bucket_objects", Title: "List or search objects in a bucket",
		Description: "List folders and objects in a bucket under an optional prefix or delimiter, search across keys with q, or list object versions and delete markers when versions is true. Paginated.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/objects"},
	}, func(ctx context.Context, c *Call, in listBucketObjectsInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if in.Limit < 0 || in.Limit > 1000 {
			return nil, errorf("limit must be between 1 and 1000.")
		}
		params := &pantech.ListBucketObjectsParams{
			Prefix:          in.Prefix,
			Delimiter:       in.Delimiter,
			Q:               in.Q,
			Cursor:          in.Cursor,
			Limit:           in.Limit,
			Versions:        in.Versions,
			KeyMarker:       in.KeyMarker,
			VersionIDMarker: in.VersionIDMarker,
		}
		list, err := c.Client.Buckets.ListObjects(ctx, in.BucketID, params)
		if err != nil {
			return nil, err
		}
		var text string
		if in.Versions {
			text = fmt.Sprintf("Bucket %s: %s.", in.BucketID, plural(len(list.Versions), "object version", "object versions"))
		} else {
			text = fmt.Sprintf("Bucket %s: %s, %s.", in.BucketID, plural(len(list.Folders), "folder", "folders"), plural(len(list.Objects), "object", "objects"))
		}
		if list.IsTruncated || list.NextCursor != "" || list.NextKeyMarker != "" {
			text += " More entries follow (use the next cursor or markers in the data)."
		}
		return ok(text, list)
	})

	add(Spec{
		Name: "get_bucket_object_meta", Title: "Get object metadata",
		Description: "Read an object's size, content type, metadata, tags, visibility, encryption and object-lock status (retention mode, retain-until time, legal hold).",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/objects/meta"},
	}, func(ctx context.Context, c *Call, in bucketObjectMetaInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if in.Key == "" {
			return nil, errorf("key is required.")
		}
		q := url.Values{"key": {in.Key}}
		setIf(q, "version_id", in.VersionID)
		var meta bucketObjectMeta
		if err := c.Client.Do(ctx, "GET", bucketPath(in.BucketID)+"/objects/meta", q, nil, &meta); err != nil {
			return nil, err
		}
		vis := "private"
		if meta.Public {
			vis = "public (" + arg(meta.PublicSource) + ")"
		}
		text := fmt.Sprintf("Object in bucket %s: %s, %s, %s.", in.BucketID, gib(meta.SizeBytes), vis, plural(len(meta.Tags), "tag", "tags"))
		if meta.LegalHold {
			text += " Legal hold is on."
		}
		if meta.RetentionMode != "" {
			text += " Retention mode: " + arg(meta.RetentionMode) + "."
		}
		return ok(text, meta)
	})

	add(Spec{
		Name: "get_bucket_cors", Title: "Get bucket CORS rules",
		Description: "Get a bucket's cross-origin resource sharing (CORS) rules.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/cors"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		cfg, err := c.Client.Buckets.GetCORS(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Bucket %s has %s.", in.BucketID, plural(len(cfg.Rules), "CORS rule", "CORS rules")), cfg)
	})

	add(Spec{
		Name: "get_bucket_lifecycle", Title: "Get bucket lifecycle rules",
		Description: "Get a bucket's lifecycle expiration and multipart cleanup rules.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/lifecycle"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		cfg, err := c.Client.Buckets.GetLifecycle(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Bucket %s has %s.", in.BucketID, plural(len(cfg.Rules), "lifecycle rule", "lifecycle rules")), cfg)
	})

	add(Spec{
		Name: "get_bucket_tags", Title: "Get bucket tags",
		Description: "Get key-value tags set on a bucket.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/tags"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		cfg, err := c.Client.Buckets.GetTags(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Bucket %s has %s.", in.BucketID, plural(len(cfg.Tags), "tag", "tags")), cfg)
	})

	add(Spec{
		Name: "get_bucket_object_lock", Title: "Get bucket object-lock retention",
		Description: "Get default object-lock retention settings (enabled, mode, days or years) for an object-lock bucket.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/object-lock"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		cfg, err := c.Client.Buckets.GetObjectLock(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		if !cfg.Enabled {
			return ok(fmt.Sprintf("Bucket %s has no default object-lock retention enabled.", in.BucketID), cfg)
		}
		dur := fmt.Sprintf("%d days", cfg.Days)
		if cfg.Years > 0 {
			dur = fmt.Sprintf("%d years", cfg.Years)
		}
		return ok(fmt.Sprintf("Bucket %s default object-lock retention is %s for %s.", in.BucketID, arg(cfg.Mode), dur), cfg)
	})

	add(Spec{
		Name: "list_bucket_shares", Title: "List bucket shares",
		Description: "List active cross-organization shares configured on a bucket.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/shares"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		var page pantech.Page[map[string]any]
		if err := c.Client.Do(ctx, "GET", bucketPath(in.BucketID)+"/shares", nil, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "bucket share", "bucket shares")
	})

	add(Spec{
		Name: "list_bucket_notifications", Title: "List bucket notifications",
		Description: "List object event notification rules (webhooks or functions subscribed to object.created and object.removed) on a bucket.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/notifications"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		var page pantech.Page[map[string]any]
		if err := c.Client.Do(ctx, "GET", bucketPath(in.BucketID)+"/notifications", nil, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "notification rule", "notification rules")
	})

	add(Spec{
		Name: "list_bucket_domains", Title: "List bucket custom domains",
		Description: "List custom domain bindings and static website settings on a bucket.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/domains"},
	}, func(ctx context.Context, c *Call, in bucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		var page pantech.Page[map[string]any]
		if err := c.Client.Do(ctx, "GET", bucketPath(in.BucketID)+"/domains", nil, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "custom domain", "custom domains")
	})

	// --- Write / Cost / Exposure / Destructive tools ---

	addConfirmed(Spec{
		Name: "update_object_storage_settings", Title: "Change object storage public access setting",
		Description: "Turn Block All Public Access on or off for the organization. When on, public bucket prefixes and public objects stop being served anonymously within seconds and making anything public is refused. When off, previously configured public prefixes and objects are served again.",
		Risk:        Exposure, Idempotent: true,
		Routes: []string{"GET /object-storage", "PATCH /object-storage/settings"},
	}, func(ctx context.Context, c *Call, in updateObjectStorageSettingsInput) (*Preview, error) {
		var cur objectStorageStatus
		if err := c.Client.Do(ctx, "GET", "/object-storage", nil, nil, &cur); err != nil {
			return nil, err
		}
		p := &Preview{
			Data: map[string]any{"current": cur, "request": map[string]any{"block_public_access": in.BlockPublicAccess}},
		}
		if in.BlockPublicAccess {
			p.Action = "Turn Block All Public Access ON for the organization's object storage."
			p.Warnings = []string{"Public bucket prefixes and public objects stop being served anonymously within seconds, and nothing can be made public until this is turned off."}
		} else {
			p.Action = "Turn Block All Public Access OFF for the organization's object storage."
			p.Warnings = []string{"Any bucket public prefixes and public objects already configured will be served publicly on the internet again."}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in updateObjectStorageSettingsInput) (*Result, error) {
		body := map[string]any{"block_public_access": in.BlockPublicAccess}
		var out objectStorageSettings
		if err := c.Client.Do(ctx, "PATCH", "/object-storage/settings", nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		state := "off"
		if out.BlockPublicAccess {
			state = "on"
		}
		return ok("Saved: Block All Public Access is "+state+".", out)
	})

	addConfirmed(Spec{
		Name: "revoke_object_storage_key", Title: "Revoke an object storage access key",
		Description: "Revoke an S3 access key immediately. Applications or tools signing requests with it stop working before this returns. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /object-storage/keys", "DELETE /object-storage/keys/{storage_key_id}"},
	}, func(ctx context.Context, c *Call, in revokeObjectStorageKeyInput) (*Preview, error) {
		if err := checkIDs("storage_key_id", in.StorageKeyID); err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Revoke object storage access key %s. This cannot be undone.", in.StorageKeyID),
			Warnings: []string{
				deleteAccessNote,
				"Anything signing S3 requests with this access key stops working immediately.",
			},
			Data: map[string]any{"storage_key_id": in.StorageKeyID},
		}
		if k, err := c.findObjectStorageKey(ctx, in.StorageKeyID); err != nil {
			return nil, err
		} else if k != nil {
			p.Data = map[string]any{"key": k}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in revokeObjectStorageKeyInput) (*Result, error) {
		if err := checkIDs("storage_key_id", in.StorageKeyID); err != nil {
			return nil, err
		}
		if err := c.Client.ObjectStorage.RevokeKey(ctx, in.StorageKeyID, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Object storage access key %s is revoked.", in.StorageKeyID),
			map[string]any{"storage_key_id": in.StorageKeyID, "status": "revoked"})
	})

	addConfirmed(Spec{
		Name: "create_bucket", Title: "Create an object storage bucket",
		Description: "Create an S3-compatible object storage bucket. Stored data is billed per GiB-month, measured hourly. Supports optional quota_bytes, versioning, object_lock (only at create; enables versioning), encryption (none or sse_s3) and public_prefixes.",
		Risk:        Cost,
		Routes:      []string{"POST /buckets", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createBucketInput) (*Preview, error) {
		body, err := createBucketBody(in)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: "Create an object storage bucket (details in the data).",
			Price:  "Stored data is billed per GiB-month, measured hourly.",
			Data:   map[string]any{"request": body},
		}
		if in.Region != "" {
			p.Action = fmt.Sprintf("Create an object storage bucket in region %s (name and settings in the data).", arg(in.Region))
		}
		if in.ObjectLock != nil && *in.ObjectLock {
			p.Warnings = append(p.Warnings, "Object lock can only be chosen at create and turns versioning on permanently; locked objects cannot be deleted until their retention expires.")
		}
		if len(in.PublicPrefixes) > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s will be readable by anyone on the internet without an access key.", plural(len(in.PublicPrefixes), "prefix", "prefixes")))
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createBucketInput) (*Result, error) {
		body, err := createBucketBody(in)
		if err != nil {
			return nil, err
		}
		var created createdObjectStorageBucket
		if err := c.Client.Do(ctx, "POST", "/buckets", nil, body, &created, c.writeOpts()...); err != nil {
			return nil, err
		}
		if created.OperationID == "" {
			return ok("Created. "+bucketText(&created.objectStorageBucket), created)
		}
		res, err := c.finish(ctx, &pantech.Accepted{OperationID: created.OperationID, ResourceID: created.ID}, "bucket "+created.ID+" created")
		if err != nil {
			return nil, err
		}
		if m, ok := res.Data.(map[string]any); ok {
			m["bucket"] = created.objectStorageBucket
		}
		return res, nil
	})

	addConfirmed(Spec{
		Name: "update_bucket", Title: "Change an object storage bucket",
		Description: "Change a bucket's quota_bytes, versioning, default encryption (none or sse_s3) or public_prefixes. Public prefixes let anyone on the internet read matching objects; an empty list makes the bucket wholly private.",
		Risk:        Exposure, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}", "PATCH /buckets/{bucket_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in updateBucketInput) (*Preview, error) {
		body, err := updateBucketBody(in)
		if err != nil {
			return nil, err
		}
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		var warnings []string
		if in.PublicPrefixes != nil {
			if len(*in.PublicPrefixes) > 0 {
				warnings = append(warnings, fmt.Sprintf("Replaces public prefixes: anyone on the internet will be able to read objects under %s without a key.", plural(len(*in.PublicPrefixes), "prefix", "prefixes")))
			} else if len(b.PublicPrefixes) > 0 {
				warnings = append(warnings, "Clears all public prefixes: anonymous reads under them will stop working.")
			}
		}
		if isFalse(in.Versioning) && b.Versioning {
			warnings = append(warnings, "Turning versioning off stops keeping new versions of overwritten or deleted objects (refused on object-lock buckets).")
		}
		return &Preview{
			Action:   fmt.Sprintf("Update bucket %s (the change is in the data).", b.ID),
			Warnings: warnings,
			Data:     map[string]any{"current": b, "change": body},
		}, nil
	}, func(ctx context.Context, c *Call, in updateBucketInput) (*Result, error) {
		body, err := updateBucketBody(in)
		if err != nil {
			return nil, err
		}
		var updated updatedObjectStorageBucket
		if err := c.Client.Do(ctx, "PATCH", bucketPath(in.BucketID), nil, body, &updated, c.writeOpts()...); err != nil {
			return nil, err
		}
		if updated.OperationID == nil || *updated.OperationID == "" {
			return ok("Saved. "+bucketText(&updated.objectStorageBucket), updated)
		}
		res, err := c.finish(ctx, &pantech.Accepted{OperationID: *updated.OperationID, ResourceID: updated.ID}, "bucket "+updated.ID+" updated")
		if err != nil {
			return nil, err
		}
		if m, ok := res.Data.(map[string]any); ok {
			m["bucket"] = updated.objectStorageBucket
		}
		return res, nil
	})

	addConfirmed(Spec{
		Name: "empty_bucket", Title: "Empty an object storage bucket",
		Description: "Permanently delete every object, previous version and delete marker in a bucket and abort incomplete multipart uploads. Objects under object lock are skipped. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}", "GET /buckets/{bucket_id}/stats", "POST /buckets/{bucket_id}/empty"},
	}, func(ctx context.Context, c *Call, in emptyBucketInput) (*Preview, error) {
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		stats, err := c.Client.Buckets.GetStats(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Permanently delete all %s (%s), all previous versions and delete markers, and abort incomplete multipart uploads in bucket %s. This cannot be undone.",
				plural(int(stats.ObjectCount), "object", "objects"), gib(stats.SizeBytes), b.ID),
			Warnings: []string{deleteAccessNote},
			Data:     map[string]any{"bucket": b, "stats": stats},
		}
		if b.ObjectLock {
			p.Warnings = append(p.Warnings, "Objects protected by object lock cannot be deleted and will remain in the bucket.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in emptyBucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		res, err := c.Client.Buckets.Empty(ctx, in.BucketID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Emptied bucket %s: %s deleted, %s aborted.",
			in.BucketID, plural(res.DeletedCount, "object", "objects"), plural(res.AbortedUploads, "multipart upload", "multipart uploads"))
		if res.LockedCount > 0 {
			text += fmt.Sprintf(" %s could not be deleted because of object lock.", plural(res.LockedCount, "object", "objects"))
		}
		return ok(text, res)
	})

	addConfirmed(Spec{
		Name: "delete_bucket", Title: "Delete an object storage bucket",
		Description: "Permanently delete a bucket. It stops serving and billing at once, and its objects cannot be recovered. A non-empty bucket is refused unless force is true; a bucket with locked objects is always refused. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}", "GET /buckets/{bucket_id}/stats", "DELETE /buckets/{bucket_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in deleteBucketInput) (*Preview, error) {
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		stats, err := c.Client.Buckets.GetStats(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Permanently delete bucket %s (%s, %s). This cannot be undone.",
				b.ID, plural(int(stats.ObjectCount), "object", "objects"), gib(stats.SizeBytes)),
			Warnings: []string{deleteAccessNote},
			Data:     map[string]any{"bucket": b, "stats": stats, "force": in.Force},
		}
		if stats.ObjectCount > 0 && !in.Force {
			p.Warnings = append(p.Warnings, "The bucket still holds objects: Pantech refuses to delete it unless force is true (or empty it first with empty_bucket).")
		}
		if stats.ObjectCount > 0 && in.Force {
			p.Warnings = append(p.Warnings, "force is true: all objects and versions in the bucket will be permanently deleted with it.")
		}
		if b.ObjectLock {
			p.Warnings = append(p.Warnings, "Object lock is enabled on this bucket: Pantech refuses to delete it while any object is locked.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in deleteBucketInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		var params *pantech.DeleteBucketParams
		if in.Force {
			params = &pantech.DeleteBucketParams{Force: true}
		}
		acc, err := c.Client.Buckets.Delete(ctx, in.BucketID, params, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "bucket "+in.BucketID+" deleted")
	})

	addConfirmed(Spec{
		Name: "set_bucket_object_visibility", Title: "Set an object's public or private visibility",
		Description: "Toggle an individual object between private and public anonymous read access without changing bucket-wide prefixes.",
		Risk:        Exposure, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}", "PUT /buckets/{bucket_id}/objects/visibility"},
	}, func(ctx context.Context, c *Call, in setBucketObjectVisibilityInput) (*Preview, error) {
		if in.Key == "" {
			return nil, errorf("key is required.")
		}
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Data: map[string]any{"bucket": b, "request": map[string]any{"key": in.Key, "version_id": in.VersionID, "public": in.Public}},
		}
		if in.Public {
			p.Action = fmt.Sprintf("Make an object in bucket %s publicly readable on the internet (key in the data).", b.ID)
			p.Warnings = []string{"Anyone on the internet will be able to download this object without credentials. Refused if Block All Public Access is on."}
		} else {
			p.Action = fmt.Sprintf("Make an object in bucket %s private (key in the data).", b.ID)
			if len(b.PublicPrefixes) > 0 {
				p.Warnings = []string{"If the object's key matches one of the bucket's public_prefixes, it stays publicly readable through the bucket prefix."}
			}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in setBucketObjectVisibilityInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if in.Key == "" {
			return nil, errorf("key is required.")
		}
		req := pantech.PutBucketObjectVisibilityRequest{Key: in.Key, Public: in.Public}
		if in.VersionID != "" {
			req.VersionID = pantech.Ptr(in.VersionID)
		}
		out, err := c.Client.Buckets.PutObjectVisibility(ctx, in.BucketID, req, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		vis := "private"
		if out.Public {
			vis = "public (" + arg(out.PublicSource) + ")"
		}
		return ok(fmt.Sprintf("Updated visibility in bucket %s: object is now %s.", in.BucketID, vis), out)
	})

	add(Spec{
		Name: "restore_bucket_object_version", Title: "Restore an object version",
		Description: "Restore a previous version of a versioned object as the new latest version, or remove a delete marker to undelete a soft-deleted object.",
		Risk:        Write,
		Routes:      []string{"POST /buckets/{bucket_id}/objects/restore-version"},
	}, func(ctx context.Context, c *Call, in restoreBucketObjectVersionInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if in.Key == "" {
			return nil, errorf("key is required.")
		}
		body := map[string]any{"key": in.Key}
		if in.VersionID != "" {
			body["version_id"] = in.VersionID
		}
		var out restoreBucketObjectVersionResult
		if err := c.Client.Do(ctx, "POST", bucketPath(in.BucketID)+"/objects/restore-version", nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Restored object version in bucket %s (%s).", in.BucketID, arg(out.Action)), out)
	})

	addConfirmed(Spec{
		Name: "delete_bucket_objects", Title: "Delete objects from a bucket",
		Description: "Delete specific objects or object versions, or every object under a recursive_prefix. Cannot be undone for non-versioned buckets or explicit version_id deletes. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}", "POST /buckets/{bucket_id}/objects/delete"},
	}, func(ctx context.Context, c *Call, in deleteBucketObjectsInput) (*Preview, error) {
		req, err := deleteBucketObjectsReq(in)
		if err != nil {
			return nil, err
		}
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		var action string
		switch {
		case len(in.Items) > 0 && in.RecursivePrefix != "":
			action = fmt.Sprintf("Delete %s and every object under the requested prefix in bucket %s (keys in the data). This cannot be undone.",
				plural(len(in.Items), "object", "objects"), b.ID)
		case in.RecursivePrefix != "":
			action = fmt.Sprintf("Recursively delete every object under the requested prefix in bucket %s (prefix in the data). This cannot be undone.", b.ID)
		default:
			action = fmt.Sprintf("Delete %s from bucket %s (keys in the data). This cannot be undone.",
				plural(len(in.Items), "object", "objects"), b.ID)
		}
		warnings := []string{deleteAccessNote}
		if b.ObjectLock {
			warnings = append(warnings, "Objects protected by object lock will fail to delete and be reported in failed.")
		}
		return &Preview{
			Action:   action,
			Warnings: warnings,
			Data:     map[string]any{"bucket": b, "request": req},
		}, nil
	}, func(ctx context.Context, c *Call, in deleteBucketObjectsInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		req, err := deleteBucketObjectsReq(in)
		if err != nil {
			return nil, err
		}
		res, err := c.Client.Buckets.DeleteObjects(ctx, in.BucketID, req, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Deleted %s from bucket %s.", plural(res.Deleted, "object", "objects"), in.BucketID)
		if len(res.Failed) > 0 {
			text += fmt.Sprintf(" %s failed.", plural(len(res.Failed), "object", "objects"))
		}
		return ok(text, res)
	})

	addConfirmed(Spec{
		Name: "set_bucket_cors", Title: "Replace bucket CORS rules",
		Description: "Replace the bucket's entire list of cross-origin resource sharing (CORS) rules. Pass an empty list to clear all CORS rules.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/cors", "PUT /buckets/{bucket_id}/cors"},
	}, func(ctx context.Context, c *Call, in setBucketCORSInput) (*Preview, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if err := checkCORSRules(in.Rules); err != nil {
			return nil, err
		}
		cur, err := c.Client.Buckets.GetCORS(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		req := corsConfigFromInput(in.Rules)
		var warnings []string
		if len(req.Rules) == 0 && len(cur.Rules) > 0 {
			warnings = append(warnings, "Passing an empty list removes all CORS rules; browser requests from other origins will be blocked.")
		}
		for _, r := range req.Rules {
			if slices.Contains(r.AllowedOrigins, "*") {
				warnings = append(warnings, "A rule allows origin \"*\": any website can make cross-origin requests matching its allowed methods.")
				break
			}
		}
		return &Preview{
			Action:   fmt.Sprintf("Replace all %s on bucket %s with %s (rules in the data).", plural(len(cur.Rules), "existing CORS rule", "existing CORS rules"), in.BucketID, plural(len(req.Rules), "new rule", "new rules")),
			Warnings: warnings,
			Data:     map[string]any{"current": cur, "new": req},
		}, nil
	}, func(ctx context.Context, c *Call, in setBucketCORSInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if err := checkCORSRules(in.Rules); err != nil {
			return nil, err
		}
		out, err := c.Client.Buckets.PutCORS(ctx, in.BucketID, corsConfigFromInput(in.Rules), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Saved %s on bucket %s.", plural(len(out.Rules), "CORS rule", "CORS rules"), in.BucketID), out)
	})

	addConfirmed(Spec{
		Name: "set_bucket_lifecycle", Title: "Replace bucket lifecycle rules",
		Description: "Replace the bucket's entire list of lifecycle expiration and cleanup rules. Active lifecycle rules automatically delete matching objects or noncurrent versions after the configured number of days.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/lifecycle", "PUT /buckets/{bucket_id}/lifecycle"},
	}, func(ctx context.Context, c *Call, in setBucketLifecycleInput) (*Preview, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if err := checkLifecycleRules(in.Rules); err != nil {
			return nil, err
		}
		cur, err := c.Client.Buckets.GetLifecycle(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		req := lifecycleConfigFromInput(in.Rules)
		var warnings []string
		for _, r := range req.Rules {
			if r.Enabled && (r.ExpirationDays > 0 || r.NoncurrentExpirationDays > 0) {
				warnings = append(warnings, "Enabled expiration rules permanently delete matching objects or noncurrent versions automatically.")
				break
			}
		}
		return &Preview{
			Action:   fmt.Sprintf("Replace all %s on bucket %s with %s (rules in the data).", plural(len(cur.Rules), "existing lifecycle rule", "existing lifecycle rules"), in.BucketID, plural(len(req.Rules), "new rule", "new rules")),
			Warnings: warnings,
			Data:     map[string]any{"current": cur, "new": req},
		}, nil
	}, func(ctx context.Context, c *Call, in setBucketLifecycleInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if err := checkLifecycleRules(in.Rules); err != nil {
			return nil, err
		}
		out, err := c.Client.Buckets.PutLifecycle(ctx, in.BucketID, lifecycleConfigFromInput(in.Rules), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Saved %s on bucket %s.", plural(len(out.Rules), "lifecycle rule", "lifecycle rules"), in.BucketID), out)
	})

	addConfirmed(Spec{
		Name: "set_bucket_tags", Title: "Replace bucket tags",
		Description: "Replace all key-value tags on a bucket. Pass an empty list to clear every tag.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/tags", "PUT /buckets/{bucket_id}/tags"},
	}, func(ctx context.Context, c *Call, in setBucketTagsInput) (*Preview, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		cur, err := c.Client.Buckets.GetTags(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		req := tagsConfigFromInput(in.Tags)
		return &Preview{
			Action: fmt.Sprintf("Replace all %s on bucket %s with %s (tags in the data).", plural(len(cur.Tags), "existing tag", "existing tags"), in.BucketID, plural(len(req.Tags), "new tag", "new tags")),
			Data:   map[string]any{"current": cur, "new": req},
		}, nil
	}, func(ctx context.Context, c *Call, in setBucketTagsInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		out, err := c.Client.Buckets.PutTags(ctx, in.BucketID, tagsConfigFromInput(in.Tags), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Saved %s on bucket %s.", plural(len(out.Tags), "tag", "tags"), in.BucketID), out)
	})

	addConfirmed(Spec{
		Name: "set_bucket_object_lock", Title: "Set bucket default object-lock retention",
		Description: "Set or disable default object-lock retention on an object-lock bucket (GOVERNANCE or COMPLIANCE mode for days or years). COMPLIANCE locks cannot be shortened or removed by anyone until expiry.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/object-lock", "PUT /buckets/{bucket_id}/object-lock"},
	}, func(ctx context.Context, c *Call, in setBucketObjectLockInput) (*Preview, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if err := checkObjectLock(in.Enabled, in.Mode, in.Days, in.Years); err != nil {
			return nil, err
		}
		cur, err := c.Client.Buckets.GetObjectLock(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		req := pantech.BucketObjectLockConfig{Enabled: in.Enabled, Mode: in.Mode, Days: in.Days, Years: in.Years}
		p := &Preview{Data: map[string]any{"current": cur, "new": req}}
		if !in.Enabled {
			p.Action = fmt.Sprintf("Disable default object-lock retention on bucket %s (existing object locks stay in place).", in.BucketID)
		} else {
			dur := fmt.Sprintf("%d days", in.Days)
			if in.Years > 0 {
				dur = fmt.Sprintf("%d years", in.Years)
			}
			p.Action = fmt.Sprintf("Set default %s object-lock retention for %s on bucket %s.", in.Mode, dur, in.BucketID)
			if in.Mode == "COMPLIANCE" {
				p.Warnings = append(p.Warnings, "COMPLIANCE mode prevents anyone—including organization owners—from deleting or shortening retention on newly uploaded objects until expiry.")
			}
			p.Warnings = append(p.Warnings, "Locked objects keep accruing storage charges and prevent the bucket from being deleted until their retention expires.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in setBucketObjectLockInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID); err != nil {
			return nil, err
		}
		if err := checkObjectLock(in.Enabled, in.Mode, in.Days, in.Years); err != nil {
			return nil, err
		}
		req := pantech.BucketObjectLockConfig{Enabled: in.Enabled, Mode: in.Mode, Days: in.Days, Years: in.Years}
		out, err := c.Client.Buckets.PutObjectLockConfig(ctx, in.BucketID, req, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		if !out.Enabled {
			return ok(fmt.Sprintf("Disabled default object-lock retention on bucket %s.", in.BucketID), out)
		}
		return ok(fmt.Sprintf("Saved default %s object-lock retention on bucket %s.", arg(out.Mode), in.BucketID), out)
	})

	addConfirmed(Spec{
		Name: "create_bucket_share", Title: "Share a bucket with another organization",
		Description: "Grant another Pantech organization read_only, write_only or read_write access to this bucket or specific key prefixes, with an optional expiry.",
		Risk:        Exposure,
		Routes:      []string{"GET /buckets/{bucket_id}", "POST /buckets/{bucket_id}/shares"},
	}, func(ctx context.Context, c *Call, in createBucketShareInput) (*Preview, error) {
		body, err := createBucketShareBody(in)
		if err != nil {
			return nil, err
		}
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		scope := "the entire bucket"
		if len(in.Prefixes) > 0 {
			scope = plural(len(in.Prefixes), "prefix", "prefixes") + " (in the data)"
		}
		p := &Preview{
			Action: fmt.Sprintf("Grant organization %s %s access to %s of bucket %s.", in.GranteeOrganizationID, in.Permissions, scope, b.ID),
			Data:   map[string]any{"bucket": b, "request": body},
		}
		if in.ExpiresAt == "" {
			p.Warnings = append(p.Warnings, "No expiry is set: the share stays active until revoked.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createBucketShareInput) (*Result, error) {
		body, err := createBucketShareBody(in)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := c.Client.Do(ctx, "POST", bucketPath(in.BucketID)+"/shares", nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Shared bucket %s (%s) with organization %s.", in.BucketID, in.Permissions, in.GranteeOrganizationID), out)
	})

	addConfirmed(Spec{
		Name: "revoke_bucket_share", Title: "Revoke a bucket share",
		Description: "Revoke another organization's cross-account share on a bucket. Their access stops immediately. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/shares", "DELETE /buckets/{bucket_id}/shares/{share_id}"},
	}, func(ctx context.Context, c *Call, in revokeBucketShareInput) (*Preview, error) {
		if err := checkIDs("bucket_id", in.BucketID, "share_id", in.ShareID); err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Revoke share %s on bucket %s. The grantee organization loses access immediately.", in.ShareID, in.BucketID),
			Warnings: []string{
				deleteAccessNote,
			},
			Data: map[string]any{"bucket_id": in.BucketID, "share_id": in.ShareID},
		}
		if item, err := c.findBucketItem(ctx, bucketPath(in.BucketID)+"/shares", in.ShareID); err != nil {
			return nil, err
		} else if item != nil {
			p.Data = map[string]any{"share": item}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in revokeBucketShareInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID, "share_id", in.ShareID); err != nil {
			return nil, err
		}
		if err := c.Client.Do(ctx, "DELETE", bucketPath(in.BucketID)+"/shares/"+in.ShareID, nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Revoked share %s on bucket %s.", in.ShareID, in.BucketID),
			map[string]any{"bucket_id": in.BucketID, "share_id": in.ShareID, "status": "revoked"})
	})

	add(Spec{
		Name: "create_bucket_notification", Title: "Create a bucket notification rule",
		Description: "Subscribe a webhook or function to object.created and/or object.removed events in a bucket, with optional prefix and suffix filters.",
		Risk:        Write,
		Routes:      []string{"POST /buckets/{bucket_id}/notifications"},
	}, func(ctx context.Context, c *Call, in createBucketNotificationInput) (*Result, error) {
		body, err := createBucketNotificationBody(in)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := c.Client.Do(ctx, "POST", bucketPath(in.BucketID)+"/notifications", nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Created %s notification rule on bucket %s for target %s.", in.TargetType, in.BucketID, in.TargetID), out)
	})

	addConfirmed(Spec{
		Name: "update_bucket_notification", Title: "Update a bucket notification rule",
		Description: "Update the events, prefix or suffix filters, or enabled state of a bucket notification rule.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/notifications", "PATCH /buckets/{bucket_id}/notifications/{notification_id}"},
	}, func(ctx context.Context, c *Call, in updateBucketNotificationInput) (*Preview, error) {
		body, err := updateBucketNotificationBody(in)
		if err != nil {
			return nil, err
		}
		data := map[string]any{"bucket_id": in.BucketID, "notification_id": in.NotificationID, "change": body}
		if item, err := c.findBucketItem(ctx, bucketPath(in.BucketID)+"/notifications", in.NotificationID); err != nil {
			return nil, err
		} else if item != nil {
			data["current"] = item
		}
		return &Preview{
			Action: fmt.Sprintf("Update notification rule %s on bucket %s (the change is in the data).", in.NotificationID, in.BucketID),
			Data:   data,
		}, nil
	}, func(ctx context.Context, c *Call, in updateBucketNotificationInput) (*Result, error) {
		body, err := updateBucketNotificationBody(in)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := c.Client.Do(ctx, "PATCH", bucketPath(in.BucketID)+"/notifications/"+in.NotificationID, nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Updated notification rule %s on bucket %s.", in.NotificationID, in.BucketID), out)
	})

	addConfirmed(Spec{
		Name: "delete_bucket_notification", Title: "Delete a bucket notification rule",
		Description: "Delete an object event notification rule from a bucket. Event deliveries for this rule stop at once. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/notifications", "DELETE /buckets/{bucket_id}/notifications/{notification_id}"},
	}, func(ctx context.Context, c *Call, in deleteBucketNotificationInput) (*Preview, error) {
		if err := checkIDs("bucket_id", in.BucketID, "notification_id", in.NotificationID); err != nil {
			return nil, err
		}
		p := &Preview{
			Action:   fmt.Sprintf("Delete notification rule %s from bucket %s. This cannot be undone.", in.NotificationID, in.BucketID),
			Warnings: []string{deleteAccessNote},
			Data:     map[string]any{"bucket_id": in.BucketID, "notification_id": in.NotificationID},
		}
		if item, err := c.findBucketItem(ctx, bucketPath(in.BucketID)+"/notifications", in.NotificationID); err != nil {
			return nil, err
		} else if item != nil {
			p.Data = map[string]any{"notification": item}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in deleteBucketNotificationInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID, "notification_id", in.NotificationID); err != nil {
			return nil, err
		}
		if err := c.Client.Do(ctx, "DELETE", bucketPath(in.BucketID)+"/notifications/"+in.NotificationID, nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Deleted notification rule %s from bucket %s.", in.NotificationID, in.BucketID),
			map[string]any{"bucket_id": in.BucketID, "notification_id": in.NotificationID, "status": "deleted"})
	})

	addConfirmed(Spec{
		Name: "create_bucket_domain", Title: "Attach a custom domain to a bucket",
		Description: "Bind a custom HTTPS hostname with a Certificate Manager TLS certificate and optional static website settings (index/error documents, SPA mode, cache TTL) to serve content from a bucket.",
		Risk:        Exposure,
		Routes:      []string{"GET /buckets/{bucket_id}", "POST /buckets/{bucket_id}/domains"},
	}, func(ctx context.Context, c *Call, in createBucketDomainInput) (*Preview, error) {
		body, err := createBucketDomainBody(in)
		if err != nil {
			return nil, err
		}
		b, err := c.getBucket(ctx, in.BucketID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action:   fmt.Sprintf("Attach custom domain to bucket %s with TLS certificate %s (hostname and website settings in the data).", b.ID, in.CertificateID),
			Warnings: []string{"Public objects in this bucket will be served over HTTPS at the custom domain once DNS points to Pantech."},
			Data:     map[string]any{"bucket": b, "request": body},
		}, nil
	}, func(ctx context.Context, c *Call, in createBucketDomainInput) (*Result, error) {
		body, err := createBucketDomainBody(in)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := c.Client.Do(ctx, "POST", bucketPath(in.BucketID)+"/domains", nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Attached custom domain to bucket %s with certificate %s.", in.BucketID, in.CertificateID), out)
	})

	addConfirmed(Spec{
		Name: "update_bucket_domain", Title: "Update a bucket custom domain",
		Description: "Update the TLS certificate or static website routing and cache settings on a bucket's custom domain.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/domains", "PATCH /buckets/{bucket_id}/domains/{domain_id}"},
	}, func(ctx context.Context, c *Call, in updateBucketDomainInput) (*Preview, error) {
		body, err := updateBucketDomainBody(in)
		if err != nil {
			return nil, err
		}
		data := map[string]any{"bucket_id": in.BucketID, "domain_id": in.DomainID, "change": body}
		if item, err := c.findBucketItem(ctx, bucketPath(in.BucketID)+"/domains", in.DomainID); err != nil {
			return nil, err
		} else if item != nil {
			data["current"] = item
		}
		return &Preview{
			Action: fmt.Sprintf("Update custom domain %s on bucket %s (the change is in the data).", in.DomainID, in.BucketID),
			Data:   data,
		}, nil
	}, func(ctx context.Context, c *Call, in updateBucketDomainInput) (*Result, error) {
		body, err := updateBucketDomainBody(in)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := c.Client.Do(ctx, "PATCH", bucketPath(in.BucketID)+"/domains/"+in.DomainID, nil, body, &out, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Updated custom domain %s on bucket %s.", in.DomainID, in.BucketID), out)
	})

	addConfirmed(Spec{
		Name: "delete_bucket_domain", Title: "Detach a bucket custom domain",
		Description: "Detach a custom domain from a bucket. HTTPS traffic to that hostname stops being served from the bucket. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /buckets/{bucket_id}/domains", "DELETE /buckets/{bucket_id}/domains/{domain_id}"},
	}, func(ctx context.Context, c *Call, in deleteBucketDomainInput) (*Preview, error) {
		if err := checkIDs("bucket_id", in.BucketID, "domain_id", in.DomainID); err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Detach custom domain %s from bucket %s. This cannot be undone.", in.DomainID, in.BucketID),
			Warnings: []string{
				deleteAccessNote,
				"HTTPS requests to that hostname will stop being served from this bucket.",
			},
			Data: map[string]any{"bucket_id": in.BucketID, "domain_id": in.DomainID},
		}
		if item, err := c.findBucketItem(ctx, bucketPath(in.BucketID)+"/domains", in.DomainID); err != nil {
			return nil, err
		} else if item != nil {
			p.Data = map[string]any{"domain": item}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in deleteBucketDomainInput) (*Result, error) {
		if err := checkIDs("bucket_id", in.BucketID, "domain_id", in.DomainID); err != nil {
			return nil, err
		}
		if err := c.Client.Do(ctx, "DELETE", bucketPath(in.BucketID)+"/domains/"+in.DomainID, nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Detached custom domain %s from bucket %s.", in.DomainID, in.BucketID),
			map[string]any{"bucket_id": in.BucketID, "domain_id": in.DomainID, "status": "deleted"})
	})
}

func (c *Call) getBucket(ctx context.Context, id string) (*objectStorageBucket, error) {
	if err := checkIDs("bucket_id", id); err != nil {
		return nil, err
	}
	var b objectStorageBucket
	if err := c.Client.Do(ctx, "GET", bucketPath(id), nil, nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Call) findObjectStorageKey(ctx context.Context, id string) (*objectStorageKey, error) {
	var page pantech.Page[objectStorageKey]
	if err := c.Client.Do(ctx, "GET", "/object-storage/keys", nil, nil, &page); err != nil {
		return nil, err
	}
	for i := range page.Data {
		if page.Data[i].ID == id {
			k := page.Data[i]
			return &k, nil
		}
	}
	return nil, nil
}

func (c *Call) findBucketItem(ctx context.Context, path, id string) (map[string]any, error) {
	var page pantech.Page[map[string]any]
	if err := c.Client.Do(ctx, "GET", path, nil, nil, &page); err != nil {
		return nil, err
	}
	for _, item := range page.Data {
		if got, _ := item["id"].(string); got == id {
			return item, nil
		}
	}
	return nil, nil
}

func createBucketBody(in createBucketInput) (map[string]any, error) {
	if err := checkBucketName(in.Name); err != nil {
		return nil, err
	}
	if err := checkOptionalIDs("region", in.Region); err != nil {
		return nil, err
	}
	if err := checkBucketEncryption(in.Encryption); err != nil {
		return nil, err
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		return nil, errorf("quota_bytes must be 0 or greater.")
	}
	if len(in.PublicPrefixes) > 20 {
		return nil, errorf("public_prefixes may have at most 20 prefixes.")
	}
	body := map[string]any{"name": in.Name}
	if in.Region != "" {
		body["region"] = in.Region
	}
	if in.QuotaBytes != nil {
		body["quota_bytes"] = *in.QuotaBytes
	}
	if in.Versioning != nil {
		body["versioning"] = *in.Versioning
	}
	if in.ObjectLock != nil {
		body["object_lock"] = *in.ObjectLock
	}
	if in.Encryption != "" {
		body["encryption"] = in.Encryption
	}
	if len(in.PublicPrefixes) > 0 {
		body["public_prefixes"] = in.PublicPrefixes
	}
	return body, nil
}

func updateBucketBody(in updateBucketInput) (map[string]any, error) {
	if err := checkIDs("bucket_id", in.BucketID); err != nil {
		return nil, err
	}
	if err := checkBucketEncryption(in.Encryption); err != nil {
		return nil, err
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		return nil, errorf("quota_bytes must be 0 or greater.")
	}
	if in.PublicPrefixes != nil && len(*in.PublicPrefixes) > 20 {
		return nil, errorf("public_prefixes may have at most 20 prefixes.")
	}
	body := map[string]any{}
	if in.QuotaBytes != nil {
		body["quota_bytes"] = *in.QuotaBytes
	}
	if in.Versioning != nil {
		body["versioning"] = *in.Versioning
	}
	if in.Encryption != "" {
		body["encryption"] = in.Encryption
	}
	if in.PublicPrefixes != nil {
		body["public_prefixes"] = *in.PublicPrefixes
	}
	if len(body) == 0 {
		return nil, errorf("Nothing to change: give quota_bytes, versioning, encryption or public_prefixes.")
	}
	return body, nil
}

func deleteBucketObjectsReq(in deleteBucketObjectsInput) (pantech.DeleteBucketObjectsRequest, error) {
	if err := checkIDs("bucket_id", in.BucketID); err != nil {
		return pantech.DeleteBucketObjectsRequest{}, err
	}
	if len(in.Items) == 0 && in.RecursivePrefix == "" {
		return pantech.DeleteBucketObjectsRequest{}, errorf("Give items or recursive_prefix.")
	}
	req := pantech.DeleteBucketObjectsRequest{}
	if len(in.Items) > 0 {
		req.Items = make([]pantech.DeleteBucketObjectItem, len(in.Items))
		for i, item := range in.Items {
			if item.Key == "" {
				return pantech.DeleteBucketObjectsRequest{}, errorf("items[%d].key is required.", i)
			}
			req.Items[i] = pantech.DeleteBucketObjectItem{Key: item.Key}
			if item.VersionID != "" {
				req.Items[i].VersionID = pantech.Ptr(item.VersionID)
			}
		}
	}
	if in.RecursivePrefix != "" {
		req.RecursivePrefix = pantech.Ptr(in.RecursivePrefix)
	}
	return req, nil
}

func corsConfigFromInput(rules []bucketCORSRuleInput) pantech.BucketCORSConfig {
	out := pantech.BucketCORSConfig{Rules: make([]pantech.BucketCORSRule, len(rules))}
	for i, r := range rules {
		ah := r.AllowedHeaders
		if ah == nil {
			ah = []string{}
		}
		eh := r.ExposeHeaders
		if eh == nil {
			eh = []string{}
		}
		out.Rules[i] = pantech.BucketCORSRule{
			ID:             r.ID,
			AllowedOrigins: r.AllowedOrigins,
			AllowedMethods: r.AllowedMethods,
			AllowedHeaders: ah,
			ExposeHeaders:  eh,
			MaxAgeSeconds:  r.MaxAgeSeconds,
		}
	}
	return out
}

func lifecycleConfigFromInput(rules []bucketLifecycleRuleInput) pantech.BucketLifecycleConfig {
	out := pantech.BucketLifecycleConfig{Rules: make([]pantech.BucketLifecycleRule, len(rules))}
	for i, r := range rules {
		out.Rules[i] = pantech.BucketLifecycleRule{
			ID:                           r.ID,
			Prefix:                       r.Prefix,
			Enabled:                      r.Enabled,
			ExpirationDays:               r.ExpirationDays,
			NoncurrentExpirationDays:     r.NoncurrentExpirationDays,
			AbortIncompleteMultipartDays: r.AbortIncompleteMultipartDays,
			ExpiredObjectDeleteMarker:    r.ExpiredObjectDeleteMarker,
		}
	}
	return out
}

func tagsConfigFromInput(tags []bucketTagInput) pantech.BucketTagsConfig {
	out := pantech.BucketTagsConfig{Tags: make([]pantech.StorageTag, len(tags))}
	for i, t := range tags {
		out.Tags[i] = pantech.StorageTag{Key: t.Key, Value: t.Value}
	}
	return out
}

func createBucketShareBody(in createBucketShareInput) (map[string]any, error) {
	if err := checkIDs("bucket_id", in.BucketID, "grantee_organization_id", in.GranteeOrganizationID); err != nil {
		return nil, err
	}
	if err := checkSharePermissions(in.Permissions); err != nil {
		return nil, err
	}
	if in.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, in.ExpiresAt); err != nil {
			return nil, errorf("expires_at must be an RFC 3339 time, e.g. 2027-01-01T00:00:00Z.")
		}
	}
	body := map[string]any{
		"grantee_organization_id": in.GranteeOrganizationID,
		"permissions":             in.Permissions,
	}
	if len(in.Prefixes) > 0 {
		body["prefixes"] = in.Prefixes
	}
	if in.ExpiresAt != "" {
		body["expires_at"] = in.ExpiresAt
	}
	return body, nil
}

func createBucketNotificationBody(in createBucketNotificationInput) (map[string]any, error) {
	if err := checkIDs("bucket_id", in.BucketID, "target_id", in.TargetID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errorf("name is required.")
	}
	if err := checkNotificationEvents(in.Events); err != nil {
		return nil, err
	}
	if err := checkNotificationTargetType(in.TargetType); err != nil {
		return nil, err
	}
	body := map[string]any{
		"name":        in.Name,
		"events":      in.Events,
		"target_type": in.TargetType,
		"target_id":   in.TargetID,
	}
	if in.Prefix != "" {
		body["prefix"] = in.Prefix
	}
	if in.Suffix != "" {
		body["suffix"] = in.Suffix
	}
	if in.Enabled != nil {
		body["enabled"] = *in.Enabled
	}
	return body, nil
}

func updateBucketNotificationBody(in updateBucketNotificationInput) (map[string]any, error) {
	if err := checkIDs("bucket_id", in.BucketID, "notification_id", in.NotificationID); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if len(in.Events) > 0 {
		if err := checkNotificationEvents(in.Events); err != nil {
			return nil, err
		}
		body["events"] = in.Events
	}
	if in.Prefix != nil {
		body["prefix"] = *in.Prefix
	}
	if in.Suffix != nil {
		body["suffix"] = *in.Suffix
	}
	if in.Enabled != nil {
		body["enabled"] = *in.Enabled
	}
	if len(body) == 0 {
		return nil, errorf("Nothing to change: give events, prefix, suffix or enabled.")
	}
	return body, nil
}

func createBucketDomainBody(in createBucketDomainInput) (map[string]any, error) {
	if err := checkIDs("bucket_id", in.BucketID, "certificate_id", in.CertificateID); err != nil {
		return nil, err
	}
	host, err := dnsName("hostname", in.Hostname)
	if err != nil {
		return nil, err
	}
	if in.CacheTTLSeconds != nil && *in.CacheTTLSeconds < 0 {
		return nil, errorf("cache_ttl_seconds must be 0 or greater.")
	}
	body := map[string]any{
		"hostname":       host,
		"certificate_id": in.CertificateID,
	}
	if in.IndexDocument != "" {
		body["index_document"] = in.IndexDocument
	}
	if in.ErrorDocument != "" {
		body["error_document"] = in.ErrorDocument
	}
	if in.SPAMode != nil {
		body["spa_mode"] = *in.SPAMode
	}
	if in.CacheTTLSeconds != nil {
		body["cache_ttl_seconds"] = *in.CacheTTLSeconds
	}
	return body, nil
}

func updateBucketDomainBody(in updateBucketDomainInput) (map[string]any, error) {
	if err := checkIDs("bucket_id", in.BucketID, "domain_id", in.DomainID); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if in.CertificateID != nil {
		if err := checkIDs("certificate_id", *in.CertificateID); err != nil {
			return nil, err
		}
		body["certificate_id"] = *in.CertificateID
	}
	if in.IndexDocument != nil {
		body["index_document"] = *in.IndexDocument
	}
	if in.ErrorDocument != nil {
		body["error_document"] = *in.ErrorDocument
	}
	if in.SPAMode != nil {
		body["spa_mode"] = *in.SPAMode
	}
	if in.CacheTTLSeconds != nil {
		if *in.CacheTTLSeconds < 0 {
			return nil, errorf("cache_ttl_seconds must be 0 or greater.")
		}
		body["cache_ttl_seconds"] = *in.CacheTTLSeconds
	}
	if len(body) == 0 {
		return nil, errorf("Nothing to change: give certificate_id, index_document, error_document, spa_mode or cache_ttl_seconds.")
	}
	return body, nil
}
