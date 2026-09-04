param(
    [string]$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path,
    [switch]$KeepCheckout
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Read-LockScalar {
    param([string]$Path, [string]$Name)

    $matches = @(Select-String -LiteralPath $Path -Pattern ("^\s*" + [regex]::Escape($Name) + ":\s*(\S+)\s*$"))
    if ($matches.Count -ne 1) {
        throw "official market lock must contain exactly one $Name value"
    }
    return $matches[0].Matches[0].Groups[1].Value
}

function Invoke-NativeText {
    param([string]$WorkingDirectory, [string]$FilePath, [string[]]$Arguments)

    Push-Location $WorkingDirectory
    try {
        $lines = & $FilePath @Arguments 2>&1
        $rendered = $lines -join "`n"
        if ($LASTEXITCODE -ne 0) {
            throw "$FilePath exited with code $LASTEXITCODE`n$rendered"
        }
        return $rendered
    }
    finally {
        Pop-Location
    }
}

function Invoke-BatchStep {
    param([string]$Name, [scriptblock]$Action)

    try {
        $output = & $Action
        $script:steps.Add([ordered]@{ name = $Name; passed = $true })
        return $output
    }
    catch {
        $message = $_.Exception.Message
        $script:steps.Add([ordered]@{ name = $Name; passed = $false; error = $message })
        $script:failures.Add("${Name}: $message")
        return $null
    }
}

$repositoryRoot = (Resolve-Path -LiteralPath $RepositoryRoot).Path
$lockPath = Join-Path $repositoryRoot 'official-market.lock'
$backendRoot = Join-Path $repositoryRoot 'panel\backend-go'
$agentRoot = Join-Path $repositoryRoot 'go-agent'
if (-not (Test-Path -LiteralPath $lockPath -PathType Leaf)) {
    throw "official market lock not found: $lockPath"
}

$repository = Read-LockScalar -Path $lockPath -Name 'repository'
$refKind = Read-LockScalar -Path $lockPath -Name 'ref_kind'
$refName = Read-LockScalar -Path $lockPath -Name 'ref_name'
if ($refKind -ne 'branch') {
    throw "official release validation requires a branch source, got $refKind"
}

$validationJSON = Invoke-NativeText -WorkingDirectory $backendRoot -FilePath 'go' -Arguments @(
    'run', './cmd/nre-plugin-validator', '--official-lock', $lockPath
)
$validation = $validationJSON | ConvertFrom-Json
$validProperty = $validation.PSObject.Properties['valid']
$commitProperty = $validation.PSObject.Properties['commit']
if ($null -eq $validProperty -or $null -eq $commitProperty -or -not $validProperty.Value -or
    [string]$commitProperty.Value -notmatch '^[0-9a-f]{40}$' -or [string]$commitProperty.Value -eq ('0' * 40)) {
    throw "official market validation did not return a valid full commit: $validationJSON"
}

$tempParent = (Resolve-Path -LiteralPath ([IO.Path]::GetTempPath())).Path.TrimEnd([char[]]@(
    [IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar
))
$tempRoot = Join-Path $tempParent ("nre-official-market-" + [guid]::NewGuid().ToString('N'))
$marketCheckout = Join-Path $tempRoot 'market'
$failures = [Collections.Generic.List[string]]::new()
$steps = [Collections.Generic.List[object]]::new()
$packageResults = [Collections.Generic.List[object]]::new()
$artifactPaths = @{}
$sourceCommit = ''
New-Item -ItemType Directory -Path $tempRoot | Out-Null

try {
    $suffix = if ($IsWindows) { '.exe' } else { '' }
    $helperPath = Join-Path $tempRoot "release-helper$suffix"
    $validatorPath = Join-Path $tempRoot "nre-plugin-validator$suffix"
    $helperSource = Join-Path $PSScriptRoot 'release_helper.go'
    Invoke-NativeText -WorkingDirectory $backendRoot -FilePath 'go' -Arguments @(
        'build', '-o', $helperPath, $helperSource
    ) | Out-Null
    Invoke-NativeText -WorkingDirectory $backendRoot -FilePath 'go' -Arguments @(
        'build', '-o', $validatorPath, './cmd/nre-plugin-validator'
    ) | Out-Null

    & git clone --quiet --depth 1 --branch $refName --single-branch $repository $marketCheckout
    if ($LASTEXITCODE -ne 0) {
        throw "git clone official market exited with code $LASTEXITCODE"
    }
    $checkoutCommit = (Invoke-NativeText -WorkingDirectory $marketCheckout -FilePath 'git' -Arguments @('rev-parse', 'HEAD')).Trim()
    if ($checkoutCommit -ne $validation.commit) {
        throw "official branch moved during validation ($($validation.commit) -> $checkoutCommit); rerun the script"
    }

    $packagesRoot = Join-Path $tempRoot 'packages'
    $preparedJSON = Invoke-NativeText -WorkingDirectory $backendRoot -FilePath $helperPath -Arguments @(
        'prepare', '--root', $marketCheckout, '--destination', $packagesRoot, '--validator', $validatorPath
    )
    $prepared = $preparedJSON | ConvertFrom-Json
    $sourceCommit = [string]$prepared.source_commit
    foreach ($result in @($prepared.package_results)) {
        $packageResults.Add($result)
        if ($result.passed) {
            $artifactPaths[$result.id] = [ordered]@{
                path = [string]$result.artifact_path
                sha256 = [string]$result.artifact_sha256
                runtime = [string]$result.runtime
            }
        }
    }
    foreach ($failure in @($prepared.failures)) {
        $failures.Add([string]$failure)
    }
    if ($prepared.packages -le 0 -or $sourceCommit -notmatch '^[0-9a-f]{40}$' -or $sourceCommit -eq ('0' * 40)) {
        $failures.Add('signed v2 projection did not contain packages and a non-zero full source OID')
    }
    Invoke-BatchStep -Name 'signed-source-commit' -Action {
        $sourceCheckout = Join-Path $tempRoot 'source'
        New-Item -ItemType Directory -Path $sourceCheckout | Out-Null
        Invoke-NativeText -WorkingDirectory $sourceCheckout -FilePath 'git' -Arguments @('init', '--quiet') | Out-Null
        Invoke-NativeText -WorkingDirectory $sourceCheckout -FilePath 'git' -Arguments @('remote', 'add', 'origin', $repository) | Out-Null
        Invoke-NativeText -WorkingDirectory $sourceCheckout -FilePath 'git' -Arguments @(
            'fetch', '--quiet', '--depth', '1', 'origin', $sourceCommit
        ) | Out-Null
        $resolvedSource = (Invoke-NativeText -WorkingDirectory $sourceCheckout -FilePath 'git' -Arguments @(
            'rev-parse', 'FETCH_HEAD'
        )).Trim()
        if ($resolvedSource -ne $sourceCommit) {
            throw "signed source OID resolved as $resolvedSource"
        }
    } | Out-Null

    foreach ($package in @($prepared.package_results | Where-Object { $_.runtime -eq 'rpc-service' })) {
        if (-not $artifactPaths.ContainsKey($package.id)) {
            $failures.Add("rpc-handshake-$($package.id): no verified artifact path")
            continue
        }
        $artifactPath = $artifactPaths[$package.id].path
        Invoke-BatchStep -Name "rpc-handshake-$($package.id)" -Action {
            $mount = "type=bind,source=$artifactPath,target=/plugin,readonly"
            $output = Invoke-NativeText -WorkingDirectory $repositoryRoot -FilePath 'docker' -Arguments @(
                'run', '--rm', '--network', 'none', '--read-only', '--mount', $mount,
                'debian:12-slim', '/plugin', '--nre-ci-rpc-handshake'
            )
            $handshake = @($output -split "`r?`n" | Where-Object { $_.Trim() -ne '' })[-1].Trim()
            if ($handshake -ne 'nre:rpc/v1') {
                throw "handshake returned $handshake"
            }
        } | Out-Null
    }

    if (-not $artifactPaths.ContainsKey('waf')) {
        $failures.Add('official-waf-performance: no verified waf artifact path')
    }
    else {
        Invoke-BatchStep -Name 'official-waf-performance' -Action {
            $overlayPath = Join-Path $tempRoot 'official-waf-overlay.json'
            $wasmRoot = Join-Path $agentRoot 'internal\plugins\wasm'
            $overlay = [ordered]@{ Replace = [ordered]@{} }
            foreach ($name in @(
                'official_waf_performance_test.go',
                'official_waf_process_memory_linux_test.go',
                'official_waf_process_memory_other_test.go',
                'official_waf_process_memory_windows_test.go'
            )) {
                $overlay.Replace[(Join-Path $wasmRoot $name)] = Join-Path $PSScriptRoot $name
            }
            $overlay | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $overlayPath -Encoding utf8
            $oldArtifact = [Environment]::GetEnvironmentVariable('NRE_OFFICIAL_WAF_ARTIFACT', 'Process')
            $oldDigest = [Environment]::GetEnvironmentVariable('NRE_OFFICIAL_WAF_SHA256', 'Process')
            try {
                $env:NRE_OFFICIAL_WAF_ARTIFACT = $artifactPaths['waf'].path
                $env:NRE_OFFICIAL_WAF_SHA256 = $artifactPaths['waf'].sha256
                $testOutput = Invoke-NativeText -WorkingDirectory $agentRoot -FilePath 'go' -Arguments @(
                    'test', '-overlay', $overlayPath, './internal/plugins/wasm',
                    '-run', '^TestOfficialWAFPerformanceGate$', '-count=1', '-v'
                )
                if ($testOutput -notmatch '(?m)^=== RUN\s+TestOfficialWAFPerformanceGate$' -or
                    $testOutput -notmatch '(?m)^--- PASS: TestOfficialWAFPerformanceGate\b' -or
                    $testOutput -match '(?m)^--- SKIP: TestOfficialWAFPerformanceGate\b|\[no tests to run\]|no tests to run') {
                    throw 'official WAF performance gate was skipped or not selected'
                }
            }
            finally {
                [Environment]::SetEnvironmentVariable('NRE_OFFICIAL_WAF_ARTIFACT', $oldArtifact, 'Process')
                [Environment]::SetEnvironmentVariable('NRE_OFFICIAL_WAF_SHA256', $oldDigest, 'Process')
            }
        } | Out-Null
    }

    Invoke-BatchStep -Name 'stable-market-branch' -Action {
        $remote = Invoke-NativeText -WorkingDirectory $repositoryRoot -FilePath 'git' -Arguments @(
            'ls-remote', '--exit-code', $repository, "refs/heads/$refName"
        )
        if ($remote -notmatch "^$checkoutCommit\s+refs/heads/$([regex]::Escape($refName))$") {
            throw "official branch moved during validation: $remote"
        }
    } | Out-Null

    [ordered]@{
        valid = $failures.Count -eq 0
        market_commit = $checkoutCommit
        source_commit = $sourceCommit
        packages = $prepared.packages
        package_results = @($packageResults)
        steps = @($steps)
        failures = @($failures)
    } | ConvertTo-Json -Depth 8
}
finally {
    if ($KeepCheckout) {
        Write-Host "checkout retained at $tempRoot"
    }
    elseif (Test-Path -LiteralPath $tempRoot) {
        $resolvedTemp = (Resolve-Path -LiteralPath $tempRoot).Path
        if (-not $resolvedTemp.StartsWith($tempParent + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            throw "refusing to remove non-temporary path $resolvedTemp"
        }
        Remove-Item -LiteralPath $resolvedTemp -Recurse -Force
    }
}

if ($failures.Count -ne 0) {
    exit 1
}
