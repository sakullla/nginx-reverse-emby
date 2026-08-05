$ErrorActionPreference = 'Stop'

$testScript = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'join-agent.test.sh'))
if (-not (Test-Path -LiteralPath $testScript -PathType Leaf)) {
    Write-Error "Join test script not found: $testScript"
    exit 2
}

$candidateRoots = [System.Collections.Generic.List[string]]::new()
$gitCommand = Get-Command git.exe -ErrorAction SilentlyContinue
if ($null -ne $gitCommand -and -not [string]::IsNullOrWhiteSpace($gitCommand.Source)) {
    $ancestor = Split-Path -Parent $gitCommand.Source
    for ($depth = 0; $depth -lt 4 -and -not [string]::IsNullOrWhiteSpace($ancestor); $depth++) {
        $candidateRoots.Add($ancestor)
        $ancestor = Split-Path -Parent $ancestor
    }
}

foreach ($root in @(
    (Join-Path ${env:ProgramFiles} 'Git'),
    $(if (${env:ProgramFiles(x86)}) { Join-Path ${env:ProgramFiles(x86)} 'Git' }),
    $(if ($env:ProgramW6432) { Join-Path $env:ProgramW6432 'Git' }),
    $(if ($env:LOCALAPPDATA) { Join-Path $env:LOCALAPPDATA 'Programs\Git' })
)) {
    if (-not [string]::IsNullOrWhiteSpace($root)) {
        $candidateRoots.Add($root)
    }
}

$shell = $null
$gitRoot = $null
foreach ($root in $candidateRoots) {
    $candidate = Join-Path $root 'usr\bin\sh.exe'
    if (Test-Path -LiteralPath $candidate -PathType Leaf) {
        $shell = [System.IO.Path]::GetFullPath($candidate)
        $gitRoot = [System.IO.Path]::GetFullPath($root)
        break
    }
}

if ([string]::IsNullOrWhiteSpace($shell)) {
    Write-Error 'Git for Windows sh.exe was not found. Install Git for Windows; sh does not need to be added to PATH.'
    exit 3
}

$portableScriptPath = $testScript.Replace('\', '/')
$previousPath = $env:PATH
$gitPathEntries = @(
    (Join-Path $gitRoot 'usr\bin'),
    (Join-Path $gitRoot 'mingw64\bin'),
    (Join-Path $gitRoot 'cmd')
) | Where-Object { Test-Path -LiteralPath $_ -PathType Container }
try {
    $env:PATH = (($gitPathEntries + @($previousPath)) -join [System.IO.Path]::PathSeparator)
    & $shell $portableScriptPath
    $testExitCode = $LASTEXITCODE
}
finally {
    $env:PATH = $previousPath
}
if ($null -eq $testExitCode) {
    $testExitCode = 1
}
exit $testExitCode
