<#
.SYNOPSIS
  Cut a new release by computing the next semver tag from your commit history and pushing it.

.DESCRIPTION
  Uses `svu` (https://github.com/caarlos0/svu) to read Conventional Commit messages since the
  last tag and decide the next version:
      fix:        -> patch   (v1.2.3 -> v1.2.4)
      feat:       -> minor   (v1.2.3 -> v1.3.0)
      feat!: / BREAKING CHANGE -> major (v1.2.3 -> v2.0.0)

  Pushing the tag triggers the `release` GitHub Actions workflow, which runs GoReleaser to
  build the cross-platform matrix, package deb/rpm/apk, and create the GitHub Release.

  This pushes the tag as a normal user push (not via GITHUB_TOKEN), so it correctly triggers
  the tag-driven release workflow.

.PARAMETER Version
  Override the computed version (e.g. -Version v0.1.0). Required for the very first release,
  since svu has no prior tag to bump from. Must start with 'v'.

.PARAMETER Force
  Skip the confirmation prompt.

.PARAMETER DryRun
  Print what would happen without creating or pushing a tag.

.EXAMPLE
  pwsh scripts/release.ps1                  # auto-compute next tag from commits
  pwsh scripts/release.ps1 -Version v0.1.0  # force the first tag
  pwsh scripts/release.ps1 -DryRun          # preview only
#>
[CmdletBinding()]
param(
    [string]$Version,
    [switch]$Force,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

function Die($msg) { Write-Error $msg; exit 1 }

# --- preconditions -----------------------------------------------------------
# Working tree must be clean: the tag should point at a committed, pushed state.
if ((git status --porcelain).Length -gt 0) {
    Die "Working tree is dirty. Commit or stash changes before releasing."
}

$branch = (git rev-parse --abbrev-ref HEAD).Trim()
if ($branch -ne 'main' -and $branch -ne 'master') {
    Write-Warning "You are on '$branch', not main/master. Releases usually come from the default branch."
    if (-not $Force) {
        $ok = Read-Host "Continue anyway? [y/N]"
        if ($ok -ne 'y') { exit 1 }
    }
}

# --- ensure svu is available -------------------------------------------------
$svu = Get-Command svu -ErrorAction SilentlyContinue
if (-not $svu) {
    # GOPATH/bin may not be on PATH; check there too before installing.
    $gobin = Join-Path (go env GOPATH) 'bin'
    $candidate = Join-Path $gobin 'svu.exe'
    if (Test-Path $candidate) {
        $svu = $candidate
    } else {
        Write-Host "svu not found — installing github.com/caarlos0/svu/v3 ..."
        go install github.com/caarlos0/svu/v3@latest
        if (-not (Test-Path $candidate)) { Die "svu install failed (looked for $candidate)." }
        $svu = $candidate
    }
} else {
    $svu = $svu.Source
}

# --- compute the next version ------------------------------------------------
if ($Version) {
    if ($Version -notmatch '^v\d+\.\d+\.\d+') { Die "Version must look like v1.2.3 (got '$Version')." }
    $next = $Version
} else {
    $next = (& $svu next).Trim()
    $current = (& $svu current 2>$null).Trim()
    if ($next -eq $current) {
        Die "No version bump warranted since $current. Use Conventional Commit prefixes (feat:/fix:) or pass -Version for the first release."
    }
}

if (git tag -l $next) { Die "Tag $next already exists." }

# --- preview -----------------------------------------------------------------
Write-Host ""
Write-Host "Next release tag: $next" -ForegroundColor Cyan
$range = (git describe --tags --abbrev=0 2>$null)
$logRange = if ($range) { "$range..HEAD" } else { "HEAD" }
Write-Host "Commits since ${range}:" -ForegroundColor Cyan
git log --pretty=format:"  %s" $logRange
Write-Host ""

if ($DryRun) { Write-Host "[dry run] Would tag $next and push it." -ForegroundColor Yellow; exit 0 }

if (-not $Force) {
    $ok = Read-Host "Create and push tag $next? [y/N]"
    if ($ok -ne 'y') { exit 1 }
}

# --- tag and push ------------------------------------------------------------
git tag -a $next -m "Release $next"
git push origin $next
Write-Host ""
Write-Host "Pushed $next. The 'release' workflow is now building it:" -ForegroundColor Green
$origin = (git remote get-url origin 2>$null) -replace '\.git$','' -replace '^git@github\.com:','https://github.com/'
if ($origin) { Write-Host "  $origin/actions" }
