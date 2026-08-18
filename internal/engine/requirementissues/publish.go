package requirementissues

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/gitlabissue"
	"github.com/deagy/cadre/cli/internal/engine/provider"
)

// Schema versions for the two files this module owns.
const (
	ItemsSchemaVersion  = 1
	LedgerSchemaVersion = 1
)

// Item is one requirement to publish.
type Item struct {
	Key         string
	Title       string
	Description string
	// ContentHash fingerprints the title and description together, so a
	// later edit to either is detectable against an already-created issue.
	ContentHash string
}

// ItemsFile is a parsed items document.
type ItemsFile struct {
	SchemaVersion int
	GateID        string
	Items         []Item
}

// ParseItemsFile reads and validates an items document.
//
// Every failure refuses the whole file rather than skipping an entry. A
// partially published set is worse than an unpublished one: the operator has
// no way to tell which items reached the project without reading it.
func ParseItemsFile(raw []byte, maxItems int) (ItemsFile, error) {
	var parsed ItemsFile

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return parsed, errorf("items is not valid JSON: %v", err)
	}

	version, _ := payload["schema_version"].(float64)
	if int(version) != ItemsSchemaVersion {
		return parsed, errorf("items schema_version must be %d, got %v",
			ItemsSchemaVersion, payload["schema_version"])
	}
	gateID, _ := payload["gate_id"].(string)
	if gateID != "G2" {
		return parsed, errorf("items gate_id must be 'G2' in v1, got %q", gateID)
	}

	rawItems, isList := payload["items"].([]any)
	if !isList || len(rawItems) == 0 {
		return parsed, errorf("items must contain a non-empty 'items' array")
	}
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	if len(rawItems) > maxItems {
		// Refusing rather than truncating: a silently shortened set publishes
		// some requirements and drops others with no signal.
		return parsed, errorf("items has %d entries, exceeding max-items %d -- refusing rather than truncating",
			len(rawItems), maxItems)
	}

	seen := map[string]bool{}
	for _, entry := range rawItems {
		object, isObject := entry.(map[string]any)
		if !isObject {
			return parsed, errorf("each entry in items must be a JSON object")
		}
		key, _ := object["key"].(string)
		if err := ValidateItemKey(key); err != nil {
			return parsed, err
		}
		if seen[key] {
			return parsed, errorf("duplicate item key %q", key)
		}
		seen[key] = true

		title, _ := object["title"].(string)
		description, _ := object["description"].(string)

		contentHash, err := provider.Fingerprint(map[string]any{
			"title": title, "description": description,
		})
		if err != nil {
			return parsed, err
		}
		parsed.Items = append(parsed.Items, Item{
			Key: key, Title: title, Description: description, ContentHash: contentHash,
		})
	}

	parsed.SchemaVersion = ItemsSchemaVersion
	parsed.GateID = gateID
	return parsed, nil
}

// SanitizedItem is an item that has passed every check and is safe to publish.
type SanitizedItem struct {
	Key         string
	Marker      string
	Label       string
	Title       string
	Body        string
	ContentHash string
}

// SanitizeItems sanitises every item, or refuses the batch.
func SanitizeItems(taskID, gateID string, items []Item) ([]SanitizedItem, error) {
	sanitized := make([]SanitizedItem, 0, len(items))
	for _, item := range items {
		title, err := SanitizeTitle(item.Title, item.Key)
		if err != nil {
			return nil, err
		}
		description, err := SanitizeDescription(item.Description, item.Key)
		if err != nil {
			return nil, err
		}
		marker := ComputeMarker(taskID, gateID, item.Key)
		label, err := ItemLabel(marker)
		if err != nil {
			return nil, err
		}
		sanitized = append(sanitized, SanitizedItem{
			Key: item.Key, Marker: marker, Label: label, Title: title,
			Body: RenderBody(taskID, gateID, marker, description), ContentHash: item.ContentHash,
		})
	}
	return sanitized, nil
}

// Eligibility is the run state a plan digest is computed against.
type Eligibility struct {
	RunHalted           bool
	RequiredReentryGate string
	GateStatus          string
	ReEntryCount        int
}

// ComputePlanDigest binds a plan to everything that could invalidate it.
//
// The digest covers the raw items bytes, the item keys and their content
// hashes, the target project, *and* the run's eligibility state including its
// re-entry count. That last part is what makes the digest a safety mechanism
// rather than a checksum: a re-entry between the dry run and the apply changes
// the digest, so the apply refuses rather than publishing requirements the run
// has since withdrawn.
func ComputePlanDigest(taskID, gateID, project string, itemsRaw []byte,
	itemKeys []string, itemHashes map[string]string, eligibility Eligibility) (string, error) {

	itemsDigest, err := provider.Fingerprint(string(itemsRaw))
	if err != nil {
		return "", err
	}
	hashes := map[string]any{}
	for key, hash := range itemHashes {
		hashes[key] = hash
	}
	keys := make([]any, 0, len(itemKeys))
	for _, key := range itemKeys {
		keys = append(keys, key)
	}

	return provider.Fingerprint(map[string]any{
		"task_id":      taskID,
		"gate_id":      gateID,
		"project":      project,
		"items_digest": itemsDigest,
		"item_keys":    keys,
		"item_hashes":  hashes,
		"eligibility": map[string]any{
			"run_halted":            eligibility.RunHalted,
			"required_reentry_gate": eligibility.RequiredReentryGate,
			"publishable":           eligibility.GateStatus != "blocked" && eligibility.GateStatus != "invalidated",
			"re_entry_count":        eligibility.ReEntryCount,
		},
	})
}

// Ledger records which items have been published, so a re-run is idempotent.
type Ledger struct {
	SchemaVersion int                    `json:"schema_version"`
	TaskID        string                 `json:"task_id"`
	GateID        string                 `json:"gate_id"`
	Project       string                 `json:"project"`
	BotUsername   string                 `json:"bot_username"`
	Mocked        bool                   `json:"mocked"`
	Entries       map[string]LedgerEntry `json:"entries"`
}

// LedgerEntry is one published item.
type LedgerEntry struct {
	IID         int    `json:"iid"`
	Marker      string `json:"marker"`
	Label       string `json:"label"`
	ContentHash string `json:"content_hash"`
	PublishedAt string `json:"published_at"`
}

func ledgerPath(root, taskID string) string {
	return filepath.Join(root, ".agentic-sdlc", "runs", taskID, "requirement-issues.json")
}

func lockPath(root, taskID string) string {
	return filepath.Join(root, ".agentic-sdlc", "runs", taskID, "requirement-issues.lock")
}

// ReadLedger loads a task's ledger, or an empty one.
func ReadLedger(root, taskID string) (Ledger, error) {
	ledger := Ledger{
		SchemaVersion: LedgerSchemaVersion, TaskID: taskID,
		Entries: map[string]LedgerEntry{},
	}
	contents, err := os.ReadFile(ledgerPath(root, taskID))
	if os.IsNotExist(err) {
		return ledger, nil
	}
	if err != nil {
		return ledger, err
	}
	if err := json.Unmarshal(contents, &ledger); err != nil {
		return ledger, errorf("requirement-issues.json is unreadable: %v", err)
	}
	if ledger.Entries == nil {
		ledger.Entries = map[string]LedgerEntry{}
	}
	return ledger, nil
}

// WriteLedger persists a task's ledger.
func WriteLedger(root string, ledger Ledger) error {
	path := ledgerPath(root, ledger.TaskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

// AcquireLock takes the publish lock for a task.
//
// Created with O_EXCL so two publishers cannot both believe they hold it, and
// never broken on a timeout: a stale-looking lock may belong to a run that is
// mid-publish, and breaking it automatically is how the same requirements get
// filed twice. Breaking it is an explicit act.
func AcquireLock(root, taskID string, breakLock bool) (string, error) {
	path := lockPath(root, taskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if breakLock {
		_ = os.Remove(path)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			holder := "(unreadable)"
			if contents, readErr := os.ReadFile(path); readErr == nil {
				holder = string(contents)
			}
			return "", Blocked{fmt.Sprintf(
				"requirement-issues.lock is already held -- pass --break-lock to override "+
					"(never auto-broken on timeout). Holder:\n%s", holder)}
		}
		return "", err
	}
	defer file.Close()

	hostname, _ := os.Hostname()
	payload, _ := json.MarshalIndent(map[string]any{
		"pid": os.Getpid(), "host": hostname, "started_at": time.Now().UTC().Format(time.RFC3339Nano),
	}, "", "  ")
	_, err = file.Write(payload)
	return path, err
}

// ReleaseLock drops the publish lock.
func ReleaseLock(path string) {
	_ = os.Remove(path)
}

// PublishRequest is one publish run.
type PublishRequest struct {
	Root       string
	TaskID     string
	GateID     string
	Project    string
	ItemsRaw   []byte
	MaxItems   int
	Apply      bool
	PlanDigest string
	AsBot      string
	BreakLock  bool
	// AcknowledgeMock must be set to apply while the GitLab backend is mocked.
	AcknowledgeMock bool
	Eligibility     Eligibility
}

// Publish plans, or applies, a batch of requirement issues.
//
// A dry run returns the plan and its digest. An apply requires that digest
// back, so the operator confirms the state they reviewed is the state being
// acted on.
func Publish(request PublishRequest) (map[string]any, error) {
	if err := CheckPublishEligibility(request.Eligibility.RunHalted,
		request.Eligibility.RequiredReentryGate, request.Eligibility.GateStatus); err != nil {
		return nil, err
	}

	parsed, err := ParseItemsFile(request.ItemsRaw, request.MaxItems)
	if err != nil {
		return nil, err
	}
	sanitized, err := SanitizeItems(request.TaskID, parsed.GateID, parsed.Items)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(parsed.Items))
	hashes := map[string]string{}
	for _, item := range parsed.Items {
		keys = append(keys, item.Key)
		hashes[item.Key] = item.ContentHash
	}
	sort.Strings(keys)

	digest, err := ComputePlanDigest(request.TaskID, parsed.GateID, request.Project,
		request.ItemsRaw, keys, hashes, request.Eligibility)
	if err != nil {
		return nil, err
	}

	if !request.Apply {
		planned := make([]any, 0, len(sanitized))
		for _, item := range sanitized {
			planned = append(planned, map[string]any{
				"key": item.Key, "marker": item.Marker, "label": item.Label, "title": item.Title,
			})
		}
		return map[string]any{
			"mode": "dry-run", "plan_digest": digest, "project": request.Project,
			"gate_id": parsed.GateID, "items": planned,
		}, nil
	}

	if request.PlanDigest == "" {
		return nil, errorf("apply requires a plan digest from a prior dry run")
	}
	if request.PlanDigest != digest {
		return nil, Blocked{fmt.Sprintf(
			"plan digest mismatch: recomputed %q != supplied %q -- state changed since the dry run "+
				"this digest came from; re-run the dry run", digest, request.PlanDigest)}
	}

	// Applying against a mock looks exactly like applying for real, right down
	// to the ledger. Requiring the operator to say so is what stops a mocked
	// run being mistaken for a published one.
	mocked := os.Getenv(gitlabissue.CreateMockEnvVar) != ""
	if mocked && !request.AcknowledgeMock {
		return nil, errorf("%s is set but the mocked-backend acknowledgement was not given -- "+
			"refusing to apply against a mocked GitLab backend", gitlabissue.CreateMockEnvVar)
	}

	// The bot identity is verified against the live credential, and the
	// confirmed name is what gets recorded -- not the one that was requested.
	verifiedUsername, err := gitlabissue.VerifyIdentity(request.AsBot)
	if err != nil {
		return nil, err
	}

	lock, err := AcquireLock(request.Root, request.TaskID, request.BreakLock)
	if err != nil {
		return nil, err
	}
	defer ReleaseLock(lock)

	ledger, err := ReadLedger(request.Root, request.TaskID)
	if err != nil {
		return nil, err
	}
	ledger.SchemaVersion = LedgerSchemaVersion
	ledger.TaskID, ledger.GateID, ledger.Project = request.TaskID, parsed.GateID, request.Project
	ledger.BotUsername, ledger.Mocked = verifiedUsername, mocked

	results := make([]any, 0, len(sanitized))
	for _, item := range sanitized {
		// Re-checked before every item, not once at the start. A re-entry or
		// invalidation part-way through means the remaining requirements are
		// no longer agreed, and continuing would file them anyway. Items
		// already created are left alone; they are recorded in the ledger.
		fresh, err := ComputePlanDigest(request.TaskID, parsed.GateID, request.Project,
			request.ItemsRaw, keys, hashes, request.Eligibility)
		if err != nil {
			return nil, err
		}
		if fresh != request.PlanDigest {
			return nil, Blocked{fmt.Sprintf(
				"plan digest changed before item %q (a reenter or invalidate happened concurrently) "+
					"-- aborting the remaining items; already-created issues are unaffected", item.Key)}
		}

		if existing, published := ledger.Entries[item.Key]; published {
			results = append(results, map[string]any{
				"key": item.Key, "status": "already-published", "iid": existing.IID,
				"content_changed": existing.ContentHash != item.ContentHash,
			})
			continue
		}

		iid, err := createIssue(request.Project, item)
		if err != nil {
			return nil, err
		}
		ledger.Entries[item.Key] = LedgerEntry{
			IID: iid, Marker: item.Marker, Label: item.Label,
			ContentHash: item.ContentHash, PublishedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := WriteLedger(request.Root, ledger); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{"key": item.Key, "status": "created", "iid": iid})
	}

	if err := WriteLedger(request.Root, ledger); err != nil {
		return nil, err
	}
	return map[string]any{
		"mode": "apply", "plan_digest": digest, "project": request.Project,
		"gate_id": parsed.GateID, "bot_username": verifiedUsername, "mocked": mocked,
		"results": results,
	}, nil
}

// createIssue searches for an existing issue by label before creating one.
//
// The label is the anchor, never the title or body: both are searchable and
// forgeable, so matching on them would let anyone in the project pre-create an
// issue this module then adopts as its own.
func createIssue(project string, item SanitizedItem) (int, error) {
	existing, err := gitlabissue.SearchIssuesByLabels(project, []string{FixedLabel, item.Label})
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		// An issue already carries this item's label, so it was created by a
		// previous run whose ledger did not survive. Adopting it rather than
		// creating a second one is what keeps publishing idempotent.
		found, _ := existing[0].(map[string]any)
		iid, _ := found["iid"].(float64)
		return int(iid), nil
	}
	return gitlabissue.CreateIssue(project, item.Title, item.Body, []string{FixedLabel, item.Label})
}

// LedgerKeys lists the published item keys, sorted.
func LedgerKeys(ledger Ledger) []string {
	keys := make([]string, 0, len(ledger.Entries))
	for key := range ledger.Entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// DescribeLedger renders a ledger for the list command.
func DescribeLedger(ledger Ledger) map[string]any {
	entries := make([]any, 0, len(ledger.Entries))
	for _, key := range LedgerKeys(ledger) {
		entry := ledger.Entries[key]
		entries = append(entries, map[string]any{
			"key": key, "iid": entry.IID, "label": entry.Label, "published_at": entry.PublishedAt,
		})
	}
	return map[string]any{
		"task_id": ledger.TaskID, "gate_id": ledger.GateID, "project": ledger.Project,
		"bot_username": ledger.BotUsername, "mocked": ledger.Mocked,
		"count": len(entries), "entries": entries,
	}
}

// ReadItemsSource reads an items document from a path, or stdin for "-".
func ReadItemsSource(value string, stdin interface{ Read([]byte) (int, error) }) ([]byte, error) {
	if value == "-" {
		if stdin == nil {
			return nil, errorf("no stdin to read items from")
		}
		var collected []byte
		buffer := make([]byte, 4096)
		for {
			read, err := stdin.Read(buffer)
			collected = append(collected, buffer[:read]...)
			if err != nil {
				if strings.Contains(err.Error(), "EOF") {
					return collected, nil
				}
				return nil, err
			}
			if read == 0 {
				return collected, nil
			}
		}
	}
	return os.ReadFile(value)
}
