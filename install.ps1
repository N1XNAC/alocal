# Alocal installer for Windows.
#   $env:ALOCAL_REPO = "OWNER/REPO"
#   irm https://raw.githubusercontent.com/OWNER/REPO/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'

$repo = $env:ALOCAL_REPO
$baseOverride = $env:ALOCAL_BASE_URL
if (-not $repo -and -not $baseOverride) { throw 'Set ALOCAL_REPO to OWNER/REPO (your GitHub repository).' }

$version = if ($env:ALOCAL_VERSION) { $env:ALOCAL_VERSION } else { 'latest' }
$installDir = if ($env:ALOCAL_INSTALL_DIR) { $env:ALOCAL_INSTALL_DIR } else { Join-Path $env:USERPROFILE '.local\bin' }

switch -Regex ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64|x86_64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  default { throw "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}
$asset = "alocal-windows-$arch.exe"
$base = if ($baseOverride) {
  $baseOverride
} elseif ($version -eq 'latest') {
  "https://github.com/$repo/releases/latest/download"
} else {
  "https://github.com/$repo/releases/download/$version"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading $asset ($version) from $repo..."
  Invoke-WebRequest -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset)
  Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

  $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match "\s$asset$" } | Select-Object -First 1
  if (-not $line) { throw "No checksum for $asset" }
  $expected = ($line -split '\s+')[0].ToLowerInvariant()
  $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLowerInvariant()
  if ($expected -ne $actual) { throw "Checksum mismatch: expected $expected, got $actual" }
  Write-Host 'Checksum verified.'

  New-Item -ItemType Directory -Path $installDir -Force | Out-Null
  Copy-Item -Path (Join-Path $tmp $asset) -Destination (Join-Path $installDir 'alocal.exe') -Force
  Write-Host "Installed $installDir\alocal.exe"

  # Remember where `alocal update` should fetch new releases.
  if ($repo -or $baseOverride) {
    $configDir = if ($env:ALOCAL_CONFIG_DIR) {
      $env:ALOCAL_CONFIG_DIR
    } elseif ($env:XDG_CONFIG_HOME) {
      Join-Path $env:XDG_CONFIG_HOME 'alocal'
    } else {
      Join-Path $env:USERPROFILE '.config\alocal'
    }
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null
    $source = @{ repo = $repo; base_url = $baseOverride } | ConvertTo-Json -Compress
    Set-Content -Path (Join-Path $configDir 'update.json') -Value $source -Encoding utf8
    Write-Host "Saved update source to $configDir\update.json"
  }

  Write-Host "Add $installDir to your PATH, then run: alocal"
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
