Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$releaseDir = Join-Path $root "release"
$sourceExe = Join-Path $root "kernforge.exe"
$statePath = Join-Path $root ".build\version-state.json"
$releaseExe = Join-Path $releaseDir "kernforge.exe"
$versionPath = Join-Path $releaseDir "kernforge-version.json"
$zipPath = Join-Path $releaseDir "kernforge.zip"
$manifestPath = Join-Path $releaseDir "release-manifest.json"
$releaseToolsDir = Join-Path $releaseDir "tools"
$releaseThirdPartyRipgrepDir = Join-Path $releaseDir "third_party\ripgrep"
$thirdPartyRipgrepDir = Join-Path $root "third_party\ripgrep"
$thirdPartyRipgrepExe = Join-Path $thirdPartyRipgrepDir "rg.exe"
$ripgrepLicenseNames = @("LICENSE-MIT", "UNLICENSE", "LICENSE")
$requiredRipgrepLicenseNames = @("LICENSE-MIT", "UNLICENSE")
$ripgrepMetadataNames = @("README.md", "NOTICE")

function Get-ReleaseVersion {
	if (Test-Path $statePath) {
		$state = Get-Content $statePath -Raw | ConvertFrom-Json
		if ($state.current_version) {
			return [string]$state.current_version
		}
	}

	if (Test-Path $sourceExe) {
		$fileVersion = (Get-Item $sourceExe).VersionInfo.FileVersion
		if (-not [string]::IsNullOrWhiteSpace($fileVersion)) {
			return [string]$fileVersion
		}
	}

	return "dev"
}

if (-not (Test-Path $sourceExe)) {
	throw "kernforge.exe was not found. Run build.ps1 first."
}

New-Item -ItemType Directory -Force $releaseDir | Out-Null

foreach ($legacyName in @("im-cli.exe", "im-cli.zip", "im-cli-version.json")) {
	$legacyPath = Join-Path $releaseDir $legacyName
	if (Test-Path $legacyPath) {
		Remove-Item -LiteralPath $legacyPath -Force
	}
}

foreach ($path in @($releaseExe, $versionPath, $zipPath, $manifestPath)) {
	if (Test-Path $path) {
		Remove-Item -LiteralPath $path -Force
	}
}
if (Test-Path $releaseToolsDir) {
	Remove-Item -LiteralPath $releaseToolsDir -Recurse -Force
}
if (Test-Path $releaseThirdPartyRipgrepDir) {
	Remove-Item -LiteralPath $releaseThirdPartyRipgrepDir -Recurse -Force
}

$version = Get-ReleaseVersion

Copy-Item -LiteralPath $sourceExe -Destination $releaseExe -Force

$versionJson = "{`n  ""version"": ""$version""`n}`n"
Set-Content -Path $versionPath -Value $versionJson -Encoding UTF8

$bundledRipgrep = Test-Path $thirdPartyRipgrepExe
$ripgrepLicenseFiles = @()
$ripgrepThirdPartyFiles = @()
foreach ($licenseName in $ripgrepLicenseNames) {
	$licensePath = Join-Path $thirdPartyRipgrepDir $licenseName
	if (Test-Path $licensePath) {
		$file = [pscustomobject]@{
			Name = $licenseName
			Path = $licensePath
		}
		$ripgrepLicenseFiles += $file
		$ripgrepThirdPartyFiles += $file
	}
}
foreach ($metadataName in $ripgrepMetadataNames) {
	$metadataPath = Join-Path $thirdPartyRipgrepDir $metadataName
	if (Test-Path $metadataPath) {
		$ripgrepThirdPartyFiles += [pscustomobject]@{
			Name = $metadataName
			Path = $metadataPath
		}
	}
}
if ($bundledRipgrep) {
	$missingRipgrepLicenses = @()
	foreach ($licenseName in $requiredRipgrepLicenseNames) {
		$licensePath = Join-Path $thirdPartyRipgrepDir $licenseName
		if (-not (Test-Path $licensePath)) {
			$missingRipgrepLicenses += $licenseName
		}
	}
	if ($missingRipgrepLicenses.Count -gt 0) {
		throw "ripgrep sidecar found at $thirdPartyRipgrepExe, but required license files are missing in $($thirdPartyRipgrepDir): $($missingRipgrepLicenses -join ', ')"
	}
	New-Item -ItemType Directory -Force $releaseToolsDir | Out-Null
	New-Item -ItemType Directory -Force $releaseThirdPartyRipgrepDir | Out-Null
	Copy-Item -LiteralPath $thirdPartyRipgrepExe -Destination (Join-Path $releaseToolsDir "rg.exe") -Force
	foreach ($file in $ripgrepThirdPartyFiles) {
		Copy-Item -LiteralPath $file.Path -Destination (Join-Path $releaseThirdPartyRipgrepDir $file.Name) -Force
	}
}

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::Open($zipPath, [System.IO.Compression.ZipArchiveMode]::Create)
try {
	[System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $releaseExe, "kernforge.exe") | Out-Null
	[System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $versionPath, "kernforge-version.json") | Out-Null
	if ($bundledRipgrep) {
		[System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $releaseToolsDir "rg.exe"), "tools/rg.exe") | Out-Null
		foreach ($file in $ripgrepThirdPartyFiles) {
			$filePath = Join-Path $releaseThirdPartyRipgrepDir $file.Name
			[System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $filePath, "third_party/ripgrep/$($file.Name)") | Out-Null
		}
	}
}
finally {
	$zip.Dispose()
}

# Emit a release-manifest.json next to the zip so the in-app self-update check
# (Phase 1: check + notify only) can compare versions and verify the artifact.
# signed_sha256 is the SHA-256 of the distributed zip.
$zipSha256 = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
$releaseDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$manifest = [ordered]@{
	latest_version = $version
	release_date   = $releaseDate
	signed_sha256  = $zipSha256
}
$manifestJson = ($manifest | ConvertTo-Json -Depth 4)
Set-Content -Path $manifestPath -Value $manifestJson -Encoding UTF8

Write-Host "Prepared release assets:"
Write-Host " - $releaseExe"
Write-Host " - $versionPath"
Write-Host " - $zipPath"
Write-Host " - $manifestPath"
if ($bundledRipgrep) {
	Write-Host " - bundled ripgrep sidecar from $thirdPartyRipgrepDir"
} else {
	Write-Host " - ripgrep sidecar not bundled; add third_party\ripgrep\rg.exe and license files to include it"
}
