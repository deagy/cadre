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

If `TAG_SIGNING_KEY` is currently a repository secret, move it: set it at the
environment scope with the command above, confirm a release still signs, then
`gh secret delete TAG_SIGNING_KEY --repo deagy/cadre` to remove the
repository-scoped copy. Leaving both in place silently keeps the weaker one
usable.

The workflow reads it into a mode-600 file under `RUNNER_TEMP` for the
duration of the job.

## 2a. What the `release` environment gates

Reaching `main` is not by itself authorization to mint a signed, published
release. The environment adds:

- a **wait timer** — a window in which a publish nobody meant to trigger can
  be cancelled from the Actions run page before signing happens. With a single
  maintainer this stands in for a required reviewer, which GitHub cannot ask
  of the person who authored the change.
- a **deployment branch policy** limiting the environment to `main` and the
  `plugin-v*` / `kernel-v*` tags, so no other ref can reach the signing key.

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
