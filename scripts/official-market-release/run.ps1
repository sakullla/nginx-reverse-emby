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
if (-not $validation.valid -or $validation.packages -ne 9 -or $validation.commit -notmatch '^[0-9a-f]{40}$') {
    throw "official market validation did not return a valid nine-package full commit: $validationJSON"
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
    & git clone --quiet --depth 1 --branch $refName --single-branch $repository $marketCheckout
    if ($LASTEXITCODE -ne 0) {
        throw "git clone official market exited with code $LASTEXITCODE"
    }
    $checkoutCommit = (Invoke-NativeText -WorkingDirectory $marketCheckout -FilePath 'git' -Arguments @('rev-parse', 'HEAD')).Trim()
    if ($checkoutCommit -ne $validation.commit) {
        throw "official branch moved during validation ($($validation.commit) -> $checkoutCommit); rerun the script"
    }

    $seenPackages = @{}
    foreach ($package in @($validation.package_details)) {
        $packageErrors = [Collections.Generic.List[string]]::new()
        if ($seenPackages.ContainsKey($package.id)) {
            $packageErrors.Add('duplicate package id')
        }
        $seenPackages[$package.id] = $true
        $packageRoot = Join-Path $marketCheckout $package.package_path
        $resolvedPackageRoot = ''
        try {
            $resolvedPackageRoot = (Resolve-Path -LiteralPath $packageRoot).Path
        }
        catch {
            $packageErrors.Add("package directory is missing: $($_.Exception.Message)")
        }
        $artifactCount = 0
        foreach ($artifact in @($package.artifacts)) {
            $artifactCount++
            try {
                $artifactPath = (Resolve-Path -LiteralPath (Join-Path $resolvedPackageRoot $artifact.path)).Path
                if (-not $artifactPath.StartsWith($resolvedPackageRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
                    throw 'artifact escaped its package directory'
                }
                $actualDigest = (Get-FileHash -LiteralPath $artifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
                $actualSize = (Get-Item -LiteralPath $artifactPath).Length
                if ($actualDigest -ne $artifact.sha256) {
                    throw "digest $actualDigest differs from $($artifact.sha256)"
                }
                if ($actualSize -ne $artifact.size) {
                    throw "size $actualSize differs from $($artifact.size)"
                }
                if ($artifact.path -eq $package.runtime_entry -or $artifactCount -eq 1) {
                    $artifactPaths[$package.id] = $artifactPath
                }
            }
            catch {
                $packageErrors.Add("artifact $($artifact.path): $($_.Exception.Message)")
            }
        }
        if ($artifactCount -eq 0) {
            $packageErrors.Add('no signed artifacts were declared')
        }
        $passed = $packageErrors.Count -eq 0
        $packageResults.Add([ordered]@{
            id = $package.id
            version = $package.version
            runtime = $package.runtime_kind
            artifacts = $artifactCount
            passed = $passed
            errors = @($packageErrors)
        })
        if (-not $passed) {
            $failures.Add("package $($package.id): $($packageErrors -join '; ')")
        }
    }
    if ($seenPackages.Count -ne 9) {
        $failures.Add("package set contains $($seenPackages.Count) unique ids, want 9")
    }

    $provenance = Get-Content -Raw -LiteralPath (Join-Path $marketCheckout 'provenance.json') | ConvertFrom-Json
    $sourceCommit = [string]$provenance.repository_commit
    if ($sourceCommit -notmatch '^[0-9a-f]{40}$') {
        $failures.Add('signed provenance repository_commit is not a full lowercase Git OID')
    }

    foreach ($package in @($validation.package_details | Where-Object { $_.runtime_kind -eq 'rpc-service' })) {
        if (-not $artifactPaths.ContainsKey($package.id)) {
            $failures.Add("rpc-handshake-$($package.id): no verified artifact path")
            continue
        }
        $artifactPath = $artifactPaths[$package.id]
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
        $waf = @($validation.package_details | Where-Object { $_.id -eq 'waf' })[0]
        $wafArtifact = @($waf.artifacts | Where-Object { $_.path -eq $waf.runtime_entry })[0]
        Invoke-BatchStep -Name 'official-waf-performance' -Action {
            $oldArtifact = [Environment]::GetEnvironmentVariable('NRE_OFFICIAL_WAF_ARTIFACT', 'Process')
            $oldDigest = [Environment]::GetEnvironmentVariable('NRE_OFFICIAL_WAF_SHA256', 'Process')
            try {
                $env:NRE_OFFICIAL_WAF_ARTIFACT = $artifactPaths['waf']
                $env:NRE_OFFICIAL_WAF_SHA256 = $wafArtifact.sha256
                $testOutput = Invoke-NativeText -WorkingDirectory $agentRoot -FilePath 'go' -Arguments @(
                    'test', './internal/plugins/wasm/...', '-run', '^TestOfficialWAFPerformanceGate$', '-count=1', '-v'
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

    [ordered]@{
        valid = $failures.Count -eq 0
        market_commit = $checkoutCommit
        source_commit = $sourceCommit
        packages = $validation.packages
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
