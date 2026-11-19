<#
.SYNOPSIS
Installs grit on Windows.

.DESCRIPTION
Downloads the release binary for this machine, checks it against the
release's checksums.txt, then runs "grit install" to start it at login.

  irm https://raw.githubusercontent.com/N1k0s1/grit/main/scripts/install.ps1 | iex

Set GRIT_VERSION (for example v0.1.0) to pick a release; the default is the
latest. GRIT_BASE_URL overrides where releases come from. Set
GRIT_NO_INSTALL=1 to download and verify only.
#>
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repo = 'N1k0s1/grit'
$Version = if ($env:GRIT_VERSION) { $env:GRIT_VERSION } else { 'latest' }

# Windows PowerShell 5.1 on older systems may not offer TLS 1.2 by default.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
switch ($cpu) {
    'AMD64' { $Arch = 'amd64' }
    'ARM64' { $Arch = 'arm64' }
    default { throw "grit install: unsupported processor $cpu" }
}

if ($Version -eq 'latest') {
    $Base = "https://github.com/$Repo/releases/latest/download"
} else {
    $Base = "https://github.com/$Repo/releases/download/$Version"
}
if ($env:GRIT_BASE_URL) { $Base = $env:GRIT_BASE_URL }
$Name = "grit-windows-$Arch.exe"

$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("grit-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
    $Exe = Join-Path $Tmp 'grit.exe'
    $Sums = Join-Path $Tmp 'checksums.txt'
    Write-Output "Downloading $Name ($Version)"
    Invoke-WebRequest -UseBasicParsing -Uri "$Base/$Name" -OutFile $Exe
    Invoke-WebRequest -UseBasicParsing -Uri "$Base/checksums.txt" -OutFile $Sums

    $Want = $null
    foreach ($Line in Get-Content $Sums) {
        $Parts = $Line -split '\s+'
        if ($Parts.Count -ge 2 -and $Parts[1] -eq $Name) { $Want = $Parts[0].ToLowerInvariant() }
    }
    if (-not $Want) { throw "grit install: checksums.txt has no entry for $Name" }
    $Got = (Get-FileHash -Algorithm SHA256 -Path $Exe).Hash.ToLowerInvariant()
    if ($Got -ne $Want) { throw "grit install: checksum mismatch for $Name. Expected $Want, got $Got. Not installing." }
    Write-Output "Checksum ok ($Got)"

    if ($env:GRIT_NO_INSTALL -eq '1') {
        Copy-Item $Exe -Destination (Join-Path (Get-Location) 'grit.exe')
        Write-Output 'Verified binary saved as .\grit.exe (GRIT_NO_INSTALL=1, nothing installed).'
    } else {
        & $Exe install
        if ($LASTEXITCODE -ne 0) { throw "grit install failed with exit code $LASTEXITCODE" }
    }
} finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
