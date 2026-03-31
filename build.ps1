Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$stateDir = Join-Path $root ".build"
$statePath = Join-Path $stateDir "version-state.json"
$outputPath = Join-Path $root "kernforge.exe"
$legacySyso = Join-Path $root "versioninfo_windows_amd64.syso"
$iconPath = Join-Path $root "kernforge.ico"
$resourceWriterPath = Join-Path $root "buildtools\VersionResourceWriter.cs"

New-Item -ItemType Directory -Force $stateDir | Out-Null
if (Test-Path $legacySyso) {
	Remove-Item $legacySyso -Force
}
if (-not (Test-Path $iconPath)) {
	throw "Required icon file was not found: $iconPath"
}
if (-not (Test-Path $resourceWriterPath)) {
	throw "Required resource writer source was not found: $resourceWriterPath"
}

function Parse-VersionParts {
	param([string]$VersionText)

	if ([string]::IsNullOrWhiteSpace($VersionText)) {
		return @(0, 0, 0, 0)
	}

	$parts = $VersionText.Trim().Split(".")
	if ($parts.Length -ne 4) {
		throw "Invalid version format: $VersionText"
	}

	$result = @()
	foreach ($part in $parts) {
		[int]$value = 0
		if (-not [int]::TryParse($part, [ref]$value)) {
			throw "Invalid version segment '$part' in $VersionText"
		}
		$result += $value
	}
	return $result
}

function Format-Version {
	param([int[]]$Parts)
	return ($Parts -join ".")
}

function Increment-Version {
	param([int[]]$Parts)

	$next = @($Parts[0], $Parts[1], $Parts[2], $Parts[3])
	$next[3]++
	if ($next[3] -gt 999) {
		$next[3] = 0
		$next[2]++
		if ($next[2] -gt 999) {
			$next[2] = 0
			$next[1]++
			if ($next[1] -gt 999) {
				$next[1] = 0
				$next[0]++
			}
		}
	}
	return $next
}

function Get-BaselineVersion {
	if (Test-Path $statePath) {
		$state = Get-Content $statePath -Raw | ConvertFrom-Json
		if ($state.current_version) {
			return [string]$state.current_version
		}
	}

	if (Test-Path $outputPath) {
		$fileVersion = (Get-Item $outputPath).VersionInfo.FileVersion
		if (-not [string]::IsNullOrWhiteSpace($fileVersion)) {
			return $fileVersion
		}
	}

	return "0.0.0.0"
}

function Get-LoadedType {
	param([string]$TypeName)

	foreach ($assembly in [AppDomain]::CurrentDomain.GetAssemblies()) {
		$type = $assembly.GetType($TypeName, $false, $false)
		if ($null -ne $type) {
			return $type
		}
	}

	return $null
}

function Get-ResourceWriterType {
	$source = Get-Content $resourceWriterPath -Raw
	$typeHash = (Get-FileHash -Path $resourceWriterPath -Algorithm SHA256).Hash.Substring(0, 12)
	$typeName = "VersionResourceWriter_$typeHash"
	$type = Get-LoadedType $typeName

	if ($null -eq $type) {
		$compiledSource = $source -replace "public static class VersionResourceWriter", "public static class $typeName"
		Add-Type -TypeDefinition $compiledSource -Language CSharp
		$type = Get-LoadedType $typeName
	}

	if ($null -eq $type) {
		throw "Failed to load resource writer type: $typeName"
	}

	return $type
}

$resourceWriterType = Get-ResourceWriterType
$baselineVersion = Get-BaselineVersion
$nextParts = Increment-Version (Parse-VersionParts $baselineVersion)
$nextVersion = Format-Version $nextParts

& go build -buildvcs=false -ldflags "-X main.appVersion=$nextVersion" -o $outputPath .
if ($LASTEXITCODE -ne 0) {
	throw "go build failed with exit code $LASTEXITCODE"
}

try {
	$resourceWriterType::Apply(
		[string]$outputPath,
		[uint16]$nextParts[0],
		[uint16]$nextParts[1],
		[uint16]$nextParts[2],
		[uint16]$nextParts[3],
		[string]$iconPath
	)
}
catch [System.Reflection.TargetInvocationException] {
	if ($_.Exception.InnerException) {
		throw $_.Exception.InnerException
	}
	throw
}

$state = [pscustomobject]@{
	current_version = $nextVersion
	updated_at      = (Get-Date).ToString("o")
}
$state | ConvertTo-Json | Set-Content -Path $statePath -Encoding UTF8

Write-Host "Built kernforge.exe with PE version $nextVersion"
