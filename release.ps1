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

foreach ($path in @($releaseExe, $versionPath, $zipPath)) {
	if (Test-Path $path) {
		Remove-Item -LiteralPath $path -Force
	}
}

$version = Get-ReleaseVersion

Copy-Item -LiteralPath $sourceExe -Destination $releaseExe -Force

$versionJson = "{`n  ""version"": ""$version""`n}`n"
Set-Content -Path $versionPath -Value $versionJson -Encoding UTF8

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::Open($zipPath, [System.IO.Compression.ZipArchiveMode]::Create)
try {
	[System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $releaseExe, "kernforge.exe") | Out-Null
	[System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $versionPath, "kernforge-version.json") | Out-Null
}
finally {
	$zip.Dispose()
}

Write-Host "Prepared release assets:"
Write-Host " - $releaseExe"
Write-Host " - $versionPath"
Write-Host " - $zipPath"
