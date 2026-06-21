# Commit Signing Setup

**Signed commits are required** in avuru-obs (see
[CONTRIBUTING.md](CONTRIBUTING.md)). `main` and `vX.Y` release branches are
intended to enforce this via branch protection ("Require signed commits"), so
unsigned commits will be rejected. This guide sets up SSH (ed25519) signing —
the simplest option. GPG also works if you prefer it.

## 1. Generate an ed25519 signing key

Use your primary verified email from <https://github.com/settings/emails>.

```bash
ssh-keygen -t ed25519 -C "<your-email>" -f ~/.ssh/github_signing_key
```

> You can press Enter twice to skip the passphrase. If you set one, you'll only
> be prompted when loading the key into your SSH agent (step 3).

## 2. Configure git to sign with SSH

```bash
git config --global user.email "<your-email>"
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/github_signing_key.pub
git config --global commit.gpgsign true
```

## 3. Add the key to your SSH agent

```bash
ssh-add ~/.ssh/github_signing_key
```

## 4. Upload the public key to GitHub

```bash
cat ~/.ssh/github_signing_key.pub
```

- Go to <https://github.com/settings/keys> → **New SSH key**
- Set the key type to **"Signing Key"** (not "Authentication Key")
- Paste the `.pub` contents and save

## 5. Enable Vigilant Mode

On the same page, toggle **"Flag unsigned commits as unverified"** so any
unsigned commit is clearly marked.

## 6. (Optional) Local verification

So `git log --show-signature` can verify your own commits:

```bash
echo "$(git config --global user.email) $(cat ~/.ssh/github_signing_key.pub)" > ~/.ssh/allowed_signers
git config --global gpg.ssh.allowedSignersFile ~/.ssh/allowed_signers
```

## 7. Verify it works

```bash
git commit --allow-empty -m "test: verify commit signing"
git log --show-signature -1
git reset HEAD~1
```

The output should include `Good "git" signature` with your email. You can also
push the test commit and look for the **Verified** badge on GitHub before
resetting.
