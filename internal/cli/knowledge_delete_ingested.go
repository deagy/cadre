package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

// delete-ingested and deletion-evidence.
//
// Both were real commands in the Python CLI, removed with it at `b418031e`
// and never rebuilt. `roster/knowledge-store/SECURITY.md` recorded the gap
// honestly and the refusal message named it, which was the right thing to do
// while it stood: what it could not do is let anyone act on a request to have
// their content removed.
//
// The capability was never missing from recall — its store has deleted by
// document id throughout. What was missing was a way to reach it without
// writing Go, which for anyone using the shipped tools is the same as not
// having it.

func knowledgeDeleteIngested(cfg *knowledge.Config, args []string) (any, error) {
	fs := flag.NewFlagSet("delete-ingested", flag.ContinueOnError)
	documentID := fs.String("id", "", "the ingested document to remove")
	reason := fs.String("reason", "", "why it is being removed; recorded as evidence")
	deletedBy := fs.String("deleted-by", "", "who is removing it")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *documentID == "" || *reason == "" || *deletedBy == "" {
		return nil, fmt.Errorf(
			"delete-ingested needs --id, --reason and --deleted-by. A removal with no stated " +
				"reason is not an audit trail, and one with no actor names nobody")
	}

	governed, err := openGovernedCorpus(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = governed.Close() }()

	// Counted before, so the evidence can say how much was removed and a
	// reader can check that against what the store now holds. A deletion
	// record asserting "gone" is worth less than one asserting a number that
	// turns out to be right.
	before, err := governed.IngestedChunkCount(*documentID)
	if err != nil {
		return nil, err
	}
	if before == 0 {
		return nil, fmt.Errorf(
			"%q holds no chunks in this store, so there is nothing to delete. "+
				"Deleting nothing and recording that it happened would put a false entry "+
				"in the evidence", *documentID)
	}

	if err := governed.DeleteIngested(*documentID); err != nil {
		return nil, err
	}

	// Confirmed after, not assumed. A delete that returns no error and leaves
	// the content in place is the failure this checks for, and nothing
	// downstream would look again.
	after, err := governed.IngestedChunkCount(*documentID)
	if err != nil {
		return nil, err
	}
	if after != 0 {
		return nil, fmt.Errorf(
			"delete-ingested removed %d of %d chunk(s) of %q and %d remain. No evidence has "+
				"been recorded: a deletion record for a partial removal would be worse than none",
			before-after, before, *documentID, after)
	}

	store, err := openStagedStore(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	deletion := knowledge.IngestedDeletion{
		DocumentID:    *documentID,
		ChunksRemoved: before,
		Reason:        *reason,
		DeletedBy:     *deletedBy,
	}
	if err := store.RecordIngestedDeletion(deletion); err != nil {
		// The content is already gone by this point, so this error is not a
		// failed command -- it is a command that half-succeeded and left no
		// trace of the half that worked. Saying only "cannot record the
		// deletion" invites the reader to run it again, which will refuse
		// because the chunks are no longer there, and the evidence will
		// simply never exist.
		return nil, fmt.Errorf(
			"%d chunk(s) of %q were removed and the evidence could NOT be written: %w\n\n"+
				"The content is gone. Re-running will refuse, because there is nothing left "+
				"to delete. Record this deletion by hand", before, *documentID, err)
	}

	recorded, err := store.IngestedDeletions(*documentID)
	if err != nil {
		return nil, err
	}
	var written any
	if len(recorded) > 0 {
		written = recorded[0]
	}
	result := map[string]any{
		"document_id":    *documentID,
		"chunks_removed": before,
		"evidence":       written,
		"note": "The content is gone and the evidence is not. The deletion record carries no " +
			"foreign key to the document, deliberately, so it outlives what it describes.",
	}
	if scope := describeDeletionScope(cfg); scope != "" {
		result["scope"] = scope
	}
	return result, nil
}

// describeDeletionScope says, where a caller can see it, that a scoped server
// credential does not scope this command.
//
// `delete-ingested` and `deletion-evidence` open the SQLite file directly.
// They never reach recall-server, so `ScopedAPIKeyAuth` and the namespace
// filter -- which do hold on the HTTP path, and were verified there -- are not
// in the way. A credential scoped to one namespace can delete another
// namespace's content through this command, and read another namespace's
// deletion records.
//
// That is a property of where the deletion path sits rather than a bug in the
// scoping, and moving it behind the server is a larger change than this goal
// took on. What was wrong was that the limit was written down only in a source
// comment: a colleague configuring a scoped key would reasonably assume it
// scopes everything the tool does, and nothing they could see said otherwise.
func describeDeletionScope(cfg *knowledge.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.Server.URL) == "" {
		return ""
	}
	return "A server credential is configured, and it does not limit this command. " +
		"delete-ingested and deletion-evidence open the store file directly rather than " +
		"going through recall-server, so namespace scoping does not apply to them: this " +
		"can delete content, and read deletion records, outside the credential's namespaces."
}

func knowledgeDeletionEvidence(cfg *knowledge.Config, args []string) (any, error) {
	fs := flag.NewFlagSet("deletion-evidence", flag.ContinueOnError)
	documentID := fs.String("id", "", "limit to one document; omit for all")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	store, err := openStagedStore(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	deletions, err := store.IngestedDeletions(*documentID)
	if err != nil {
		return nil, err
	}
	verification := make([]any, 0, len(deletions))
	for _, d := range deletions {
		verification = append(verification, map[string]any{
			"deletion":           d,
			"actor_verification": describeActorVerification(d.ObservedActor),
		})
	}
	out := map[string]any{
		"deletions": verification,
		"note": "deleted_by is a string the caller supplied. actor_verification says whether " +
			"anything checked it; see `deletion-evidence-staged` for the staged half.",
	}
	if scope := describeDeletionScope(cfg); scope != "" {
		out["scope"] = scope
	}
	return out, nil
}
