# Setting up tag signing

One-time setup. Until it is done, releases still work — they simply produce
unsigned annotated tags and the workflow logs a warning, rather than failing.

## 1. Generate a signing key

Run this yourself. Do not have anyone generate it for you and paste it into
a chat or an issue; the private key should never leave your machine except
into the GitHub secret.

```sh
ssh-keygen -t ed25519 -f ~/.ssh/cadre-tag-signing -C "releases@cadre" -N ""
```

## 2. Store the private key as a `release` **environment** secret

```sh
gh secret set TAG_SIGNING_KEY --repo deagy/cadre --env release < ~/.ssh/cadre-tag-signing
```

Scope it to the environment, not the repository. Both release jobs declare
`environment: release`, so an environment-scoped secret is readable only by a
job that has passed that environment's gate. A repository-scoped secret is
readable by *any* job in *any* workflow, including one added by a future
pull request, which is the difference between a gate and a suggestion.

**If `TAG_SIGNING_KEY` is currently a repository secret, moving it is the
security control, not a tidy-up.** Until it moves, the environment gate
constrains only the two jobs that opt into it; the key itself stays readable
by any job in any workflow on any branch. Set it at the environment scope with
the command above, confirm a release still signs, then
`gh secret delete TAG_SIGNING_KEY --repo deagy/cadre` to remove the
repository-scoped copy. Do it in that order — deleting first would fail the
next release — and do not skip the delete: leaving both in place silently
keeps the weaker one usable, which is the same as not having moved it.

The workflow reads it into a mode-600 file under `RUNNER_TEMP` for the
duration of the job.

## 2a. What the `release` environment gates

Reaching `main` is not by itself authorization to mint a signed, published
release. The environment adds:

- **required approval.** The run stops and waits for you to click Approve on
  the Actions run page. It waits indefinitely — it does not elapse and publish
  on its own.

  A wait timer was the first design here, on the belief that a solo maintainer
  could not use required reviewers because GitHub forbids approving your own
  work. That is true of *pull request* review and not of *environment*
  deployments, which permit self-approval (the `prevent_self_review` flag
  exists precisely to turn it off, and is off here). An approval is strictly
  better than a timer for the case this guards: a timer that nobody is
  watching elapses and publishes anyway, which is the exact unattended release
  it was supposed to prevent.

  Approving is a deliberate act, so treat it as the review step: check the
  version, the changelog entry, and that the trigger was intended before
  clicking. Nothing else in the pipeline asks you that question.

  Cancelling instead of approving is clean — the job never starts, so nothing
  is signed, tagged, or published. Cancelling *after* approval is not, and the
  two jobs differ. The kernel job builds, checksums, and attests before it
  tags, so an interruption leaves at worst a tag with no release page. The
  plugin job tags first and generates its SBOM and attestation afterwards, so
  an interruption there can leave a signed, pushed `plugin-v*` tag with no
  GitHub Release and no attestation.

  That state does not heal itself, and re-running is not enough: the
  "Skip if this version is already tagged" step finds the tag and skips every
  later step, including publishing, so the retry succeeds while doing nothing.
  Recovery is to delete the remote tag first (`git push --delete origin
  <tag>`), then re-run — or publish a release against the existing tag by
  hand.
- a **deployment branch policy** limiting the environment to `main` and the
  `plugin-v*` / `kernel-v*` tags, so no other ref can start a job that
  requests this environment.

**What it does not do while `TAG_SIGNING_KEY` is a repository secret.** Neither
rule protects the *secret* — they gate the two jobs that opt into the
environment. A job that simply omits `environment:` reads a repository secret
directly, on any branch, with no approval and no branch restriction, never
touching this gate at all. For the purpose of protecting the key from an
unintended path, the gate currently provides nothing; it only adds friction to
the two jobs in `release.yml`. Step 2's migration is what changes that.

**One operational consequence.** The branch policy admits only `main` and the
two tag patterns, so `workflow_dispatch` from a feature branch no longer runs
the release jobs — including a dry run of a change to `release.yml` itself,
which can only be exercised from `main` after merging. Dispatching against an
existing release tag is allowed, which is the path for retrying a stalled
release for a version that is already tagged.

Both are repository settings, not files in this tree, so they are not covered
by any test here. Check them at
`https://github.com/deagy/cadre/settings/environments` if a release behaves
unexpectedly.

## 3. Register the public key on your account, as a signing key

This is what makes GitHub display the tags as **Verified**. It must be added
as a *signing* key — an authentication key of the same name does not count.

```sh
gh ssh-key add ~/.ssh/cadre-tag-signing.pub --type signing --title "cadre tag signing"
```

## 4. Commit the public key so others can verify locally

```sh
cp ~/.ssh/cadre-tag-signing.pub .github/tag-signing-key.pub
git add .github/tag-signing-key.pub && git commit -m "chore: publish the tag signing public key"
```

Public keys are safe to commit; this is the file SECURITY.md points readers
at to build an `allowed_signers` entry.

## 5. Cut a release and check

Bump a version, let the workflow run, then confirm both:

```sh
git fetch --tags
git cat-file -p plugin-v<version> | grep -c "BEGIN SSH SIGNATURE"   # 1
gh api repos/deagy/cadre/git/ref/tags/plugin-v<version> --jq .object.sha \
  | xargs -I{} gh api repos/deagy/cadre/git/tags/{} --jq .verification
```

`verification.verified` should be `true`. If it is `false` with reason
`unknown_key`, step 3 was missed or the key was added as an auth key.

The workflow also verifies its own signature before pushing, and **fails the
release** if it does not check out — a tag that cannot be verified is the
defect, not something to log and continue past.

## Rotating

Rotate on a schedule, not only on suspicion of compromise. Record the date of
the last rotation here when you do it, so the next reader can tell whether one
is overdue:

| Last rotated | By |
|---|---|
| *(not yet recorded)* | |

Repeat steps 1–4 with a new key and remove the old one from your account.
Tags signed with the retired key stop verifying against the current
`allowed_signers`, which is the expected cost of rotation; the tag objects
themselves are unchanged.
