<#
.SYNOPSIS
Installs the gemini-image binary for Windows from the GitHub releases.

.DESCRIPTION
Downloads gemini-image for this system (Windows, amd64 or arm64), checks it
against the release's SHA-256 checksums and installs it in -Dir. Prints the
path of the binary on stdout; progress goes to stderr. If -Dir already has
that version, nothing is downloaded.

Exit codes: 0 installed or already there, 1 download or check failed,
2 bad arguments or unsupported system. On macOS and Linux, use install.sh.

.EXAMPLE
powershell -ExecutionPolicy Bypass -File scripts\install.ps1

.EXAMPLE
powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Version v1.0.0 -Dir C:\Tools
#>
param(
    # Release to install, e.g. v1.0.0 (default: the latest).
    [string]$Version = 'latest',
    # Where to install (default: %LOCALAPPDATA%\Programs\gemini-image).
    [string]$Dir,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # the progress bar makes Invoke-WebRequest very slow
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# GEMINI_IMAGE_REPO_URL points the script at another server (tests, mirrors).
$repoUrl = if ($env:GEMINI_IMAGE_REPO_URL) { $env:GEMINI_IMAGE_REPO_URL } else { 'https://github.com/ehedenst/gemini-image' }
$goInstall = 'with Go installed, run: go install github.com/ehedenst/gemini-image@latest'

function Fail([string]$Message, [int]$Code = 1) {
    [Console]::Error.WriteLine("error: $Message")
    exit $Code
}

function Note([string]$Message) {
    [Console]::Error.WriteLine($Message)
}

if ($Help) {
    Note @"
Usage: install.ps1 [-Version vX.Y.Z] [-Dir DIR]

Download gemini-image for Windows from $repoUrl/releases, check it against
the release's SHA-256 checksums and install it in DIR. Prints the path of the
binary on stdout; progress goes to stderr. If DIR already has that version,
nothing is downloaded.

  -Version vX.Y.Z  release to install (default: the latest)
  -Dir DIR         where to install (default: %LOCALAPPDATA%\Programs\gemini-image)

Exit codes: 0 installed or already there, 1 download or check failed,
2 bad arguments or unsupported system.
"@
    exit 0
}

if ($PSVersionTable.PSVersion.Major -ge 6 -and -not $IsWindows) {
    Fail 'on macOS and Linux, run: sh scripts/install.sh' 2
}
if (-not $Dir) { $Dir = Join-Path $env:LOCALAPPDATA 'Programs\gemini-image' }
$osArch = try { [string][Runtime.InteropServices.RuntimeInformation]::OSArchitecture } catch { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($osArch) {
    { $_ -in 'X64', 'AMD64' } { 'amd64' }
    'Arm64' { 'arm64' }
    default { Fail "no release for $osArch Windows; $goInstall" 2 }
}

if ($Version -match '^\d') { $Version = "v$Version" }
if ($Version -eq 'latest') {
    # github.com/OWNER/REPO/releases/latest redirects to .../releases/tag/vX.Y.Z.
    try {
        $request = [Net.WebRequest]::Create("$repoUrl/releases/latest")
        $request.Method = 'HEAD'
        $response = $request.GetResponse()
        $url = $response.ResponseUri.AbsoluteUri
        $response.Close()
    } catch {
        Fail "can't reach $repoUrl/releases/latest: $($_.Exception.Message)"
    }
    if ($url -notmatch '/releases/tag/([^/]+)$') { Fail "no release found at $repoUrl/releases; $goInstall" }
    $Version = $Matches[1]
}
if ($Version -notmatch '^v\d+\.\d+\.\d+$') { Fail "-Version must look like v1.2.3, got `"$Version`"" 2 }

$exe = Join-Path $Dir 'gemini-image.exe'
$current = if (Test-Path $exe) { try { & $exe --version 2>$null } catch { $null } }
if ($current -eq "gemini-image version $Version") {
    Note "gemini-image $Version is already installed"
    Write-Output $exe
    exit 0
}

$name = "gemini-image_${Version}_windows_$arch"
$download = "$repoUrl/releases/download/$Version"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory $tmp | Out-Null
try {
    Note "downloading $download/$name.zip"
    try {
        Invoke-WebRequest -UseBasicParsing "$download/$name.zip" -OutFile "$tmp\$name.zip"
    } catch {
        Fail "can't download $name.zip ($($_.Exception.Message)); see $repoUrl/releases for the available versions"
    }
    try {
        Invoke-WebRequest -UseBasicParsing "$download/checksums.txt" -OutFile "$tmp\checksums.txt"
    } catch {
        Fail "can't download checksums.txt for $Version ($($_.Exception.Message))"
    }
    $want = Get-Content "$tmp\checksums.txt" | ForEach-Object {
        $hash, $file = -split $_
        if ($file -eq "$name.zip") { $hash }
    } | Select-Object -First 1
    if (-not $want) { Fail "checksums.txt of $Version has no entry for $name.zip" }
    $got = (Get-FileHash -Algorithm SHA256 "$tmp\$name.zip").Hash.ToLowerInvariant()
    if ($got -ne $want) { Fail "checksum mismatch for ${name}.zip: got $got, want $want; nothing installed" }

    Expand-Archive "$tmp\$name.zip" -DestinationPath $tmp
    New-Item -ItemType Directory -Force $Dir | Out-Null
    # Copy next to the target, then rename, so a half-copied binary is never left behind.
    Copy-Item "$tmp\$name\gemini-image.exe" "$exe.tmp" -Force
    Move-Item "$exe.tmp" $exe -Force
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
try { & $exe --version | Out-Null } catch { Fail "installed $exe, but it doesn't run: $($_.Exception.Message)" }
if ($LASTEXITCODE -ne 0) { Fail "installed $exe, but it doesn't run" }

Note "installed gemini-image $Version"
if (($env:Path -split ';') -notcontains $Dir) {
    Note "note: $Dir is not on PATH; run gemini-image as $exe"
}
Write-Output $exe
