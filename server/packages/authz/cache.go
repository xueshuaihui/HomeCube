package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// CacheKey represents the cache key structure per PRD 15.6
type CacheKey struct {
	AccountID string
	FamilyID  string
}

// CacheEntry represents a cached permission snapshot
type CacheEntry struct {
	Claims    *Claims
	Snapshot  *PermissionSnapshot
	CreatedAt time.Time
	TTL       time.Duration
}

// PermissionSnapshot represents the permission snapshot response structure
// Per PRD 15.2 and tech plan §4.2, P1 returns empty arrays for family_overrides and object_acls
type PermissionSnapshot struct {
	PVersion        int64             `json:"pver"`
	Permissions     map[string]string `json:"permissions"`      // resource -> action level
	FamilyOverrides []FamilyOverride  `json:"family_overrides"` // P1: always empty array
	ObjectACLs      []ObjectACL       `json:"object_acls"`      // P1: always empty array
	ModuleConfig    []ModuleConfig    `json:"module_config"`    // Enabled modules from homeos_family_module
}

// FamilyOverride represents family-level permission overrides (P6 feature, P1 placeholder)
type FamilyOverride struct {
	Resource   string `json:"resource"`
	Action     string `json:"action"`
	Role       string `json:"role"`
	Permission string `json:"permission"`
}

// ObjectACL represents object-level ACLs (P6 feature, P1 placeholder)
type ObjectACL struct {
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	Role       string `json:"role"`
	Permission string `json:"permission"`
}

// ModuleConfig represents a module's configuration status
type ModuleConfig struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Born         bool   `json:"born"`
	Availability string `json:"availability"` // "available" | "unavailable"
}

// Cache implements in-memory permission cache with TTL per PRD 15.6
// TTL: 60 seconds
// Key: (account_id, fid)
// Invalidation: subscribes to homeos.permission.updated and homeos.family.module.updated
type Cache struct {
	mu       sync.RWMutex
	items    map[CacheKey]*CacheEntry
	ttl      time.Duration
	natsConn *nats.Conn
	js       jetstream.JetStream
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewCache creates a new permission cache
func NewCache(ttl time.Duration) *Cache {
	if ttl == 0 {
		ttl = 60 * time.Second // Default TTL per PRD 15.6
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Cache{
		items:  make(map[CacheKey]*CacheEntry),
		ttl:    ttl,
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetNATSConnection sets up NATS connection for cache invalidation
func (c *Cache) SetNATSConnection(conn *nats.Conn, js jetstream.JetStream) {
	c.natsConn = conn
	c.js = js
	c.startSubscribers()
}

// Get retrieves a cached entry by account_id and family_id
// Returns nil if not found or expired
func (c *Cache) Get(accountID, familyID string) (*PermissionSnapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := CacheKey{AccountID: accountID, FamilyID: familyID}
	entry, exists := c.items[key]
	if !exists {
		return nil, false
	}

	// Check expiration
	if time.Since(entry.CreatedAt) > entry.TTL {
		// Expired, remove and return miss
		delete(c.items, key)
		return nil, false
	}

	return entry.Snapshot, true
}

// Set stores a permission snapshot in cache
func (c *Cache) Set(accountID, familyID string, claims *Claims, snapshot *PermissionSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := CacheKey{AccountID: accountID, FamilyID: familyID}
	c.items[key] = &CacheEntry{
		Claims:    claims,
		Snapshot:  snapshot,
		CreatedAt: time.Now(),
		TTL:       c.ttl,
	}
}

// Invalidate removes a specific cache entry
func (c *Cache) Invalidate(accountID, familyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := CacheKey{AccountID: accountID, FamilyID: familyID}
	delete(c.items, key)
}

// InvalidateByFamily invalidates all cache entries for a given family_id
// Called when homeos.permission.updated or homeos.family.module.updated is received
func (c *Cache) InvalidateByFamily(familyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.items {
		if key.FamilyID == familyID {
			delete(c.items, key)
		}
	}
}

// Clear clears all cache entries
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[CacheKey]*CacheEntry)
}

// startSubscribers starts NATS durable consumers for cache invalidation events
// Subscribes to: homeos.permission.updated, homeos.family.module.updated
func (c *Cache) startSubscribers() {
	if c.natsConn == nil || c.js == nil {
		return
	}

	go func() {
		// Subscribe to permission updates
		c.subscribeToSubject("homeos.permission.updated", c.handlePermissionUpdate)
		// Subscribe to module config updates
		c.subscribeToSubject("homeos.family.module.updated", c.handleModuleUpdate)
	}()
}

// subscribeToSubject creates a durable consumer for a subject
func (c *Cache) subscribeToSubject(subject string, handler func(msg jetstream.Msg)) {
	if c.js == nil {
		return
	}

	// Create or get stream
	stream, err := c.js.Stream(c.ctx, "HC_HOMEOS")
	if err != nil {
		// Stream may not exist yet, log and return
		fmt.Printf("authz cache: failed to get stream HC_HOMEOS: %v\n", err)
		return
	}

	// Create durable consumer
	consumerName := fmt.Sprintf("authz-cache-%s", subject)
	consumer, err := stream.CreateOrUpdateConsumer(c.ctx, jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    4,
	})
	if err != nil {
		fmt.Printf("authz cache: failed to create consumer for %s: %v\n", subject, err)
		return
	}

	// Start consuming messages
	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			default:
				msgs, err := consumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
				if err != nil {
					time.Sleep(1 * time.Second)
					continue
				}

				for msg := range msgs.Messages() {
					handler(msg)
					msg.Ack()
				}
			}
		}
	}()
}

// handlePermissionUpdate handles homeos.permission.updated events
// Invalidates cache for the affected family
func (c *Cache) handlePermissionUpdate(msg jetstream.Msg) {
	// Parse message to extract family_id
	// Message format: {"family_id": "...", "pver": ...}
	var payload struct {
		FamilyID string `json:"family_id"`
		PVersion int64  `json:"pver"`
	}

	data := msg.Data()
	if err := json.Unmarshal(data, &payload); err != nil {
		fmt.Printf("authz cache: failed to parse permission update: %v\n", err)
		return
	}

	if payload.FamilyID != "" {
		c.InvalidateByFamily(payload.FamilyID)
	}
}

// handleModuleUpdate handles homeos.family.module.updated events
// Invalidates cache for the affected family
func (c *Cache) handleModuleUpdate(msg jetstream.Msg) {
	// Parse message to extract family_id
	// Message format: {"family_id": "...", "code": "...", "version": ...}
	var payload struct {
		FamilyID string `json:"family_id"`
		Code     string `json:"code"`
		Version  int64  `json:"version"`
	}

	data := msg.Data()
	if err := json.Unmarshal(data, &payload); err != nil {
		fmt.Printf("authz cache: failed to parse module update: %v\n", err)
		return
	}

	if payload.FamilyID != "" {
		c.InvalidateByFamily(payload.FamilyID)
	}
}

// Stop stops the cache and its subscribers
func (c *Cache) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// DegradeReadAllowed implements the degradation strategy per tech plan §4.2:
// "取不到快照 → 拒绝写、允许读已缓存"
// This function should be called when homeos is unreachable
// Returns true if read operations are allowed with cached data
func DegradeReadAllowed() bool {
	// In P1, we allow reads with cached data when SDK cannot reach homeos
	// Write operations should be rejected
	return true
}

// DegradeWriteAllowed implements the degradation strategy:
// Returns false - writes are not allowed when SDK cannot reach homeos
func DegradeWriteAllowed() bool {
	return false
}

// Size returns the current number of cached entries
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}
