# Updating Kilo Proxy

Desktop checks the latest stable GitHub release at startup and every six hours. **Settings → App updates** also lets you check manually. Checking uses no Kilo, ChatGPT or GitHub credentials and does not install anything.

## Installed with Homebrew or APT

When Kilo Proxy verifies that the running executable belongs to its Homebrew or APT package, a newer release offers **Update and restart**. Review the confirmation and accept when your requests have finished.

The app opens a visible terminal and prepares a separate private updater before closing. The updater waits for the running profile to close, refreshes the package catalog, upgrades the selected Kilo Proxy package, checks the installed version and package ownership, and starts the desktop with the same configuration directory. Accounts, model settings and chats stay in their existing profile. The proxy is unavailable while it is closed.

| Installation | Upgrade performed |
| --- | --- |
| Homebrew macOS Desktop | `brew update`, then `brew upgrade --cask rosseca/tap/kilo-proxy` |
| Homebrew Linux Desktop | `brew update`, then `brew upgrade rosseca/tap/kilo-proxy-desktop` |
| APT Ubuntu Desktop | `sudo apt-get update`, then `sudo apt-get install --only-upgrade kilo-proxy-desktop=VERSION` |

APT asks for your administrator password in that terminal when needed. Kilo Proxy and its updater stay under your ordinary user account; only the package-manager commands use `sudo`. Homebrew's Linux desktop package builds from source and can take longer. Package managers may update dependencies required by the selected package; the updater does not request a system-wide upgrade.

The installation is identified from its actual executable path and package records. A manually downloaded copy is not upgraded merely because the machine also has Homebrew or APT installed. If several copies exist, only the package owning the running copy is selected.

The Homebrew tap and APT repository are published after the GitHub release. If the selected version has not reached the package feed yet, the updater stops rather than claiming success. APT also refuses an ambiguous version advertised by another repository. Failed installations remain visible in the terminal and the next desktop startup shows a failed-update notice. The updater does not automatically restart an unverified or failed installation; reopen the app manually after addressing the terminal error.

Existing terminal-agent wrappers are refreshed only when their complete contents match this installation and profile. Updating does not create missing wrappers, edit shell startup files, switch another profile's wrappers, or create a service.

The restarted desktop retains the session's absolute CLI search paths, including tools installed through nvm or asdf. Package-manager commands use a separate fixed search path; provider credentials, code-injection variables and manager-only flags are not passed to the restarted app.

## Headless

Run the commands from your normal terminal, preserving the same `--config-dir` if you use one:

```sh
kilo-proxy-headless update check
kilo-proxy-headless update install
```

`update install` asks for confirmation; `--yes` explicitly confirms it for a script. It uses the matching `kilo-proxy-headless` Homebrew or APT package and checks the result. Stop the profile before installing. The command refuses a busy profile and does not restart `serve` automatically.

If this profile has a Kilo Proxy user service installed, first stop and uninstall that service, then update and reinstall it using the same configuration directory. This avoids leaving a Homebrew service pointing at a removed versioned executable. Service uninstall preserves the profile. See [headless service management](headless.md).

## Downloaded archives and Windows

These installations continue to offer **Download update**. Download the correct archive from the linked stable release, quit Kilo Proxy, replace the application, and reopen it. This release does not silently replace manually installed files or add a Windows package manager. Homebrew/APT installation and updating do not add publisher signing or macOS notarization.

See [Ubuntu APT](ubuntu-apt.md) and the [Homebrew tap](https://github.com/rosseca/homebrew-tap#updates) for equivalent manual commands and installation requirements.
