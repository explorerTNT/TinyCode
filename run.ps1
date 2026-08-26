# tiny-code — удобный запуск
param(
    [string]$prompt = "",
    [string]$permission = "auto",
    [string]$model = "",
    [string]$workspace = ""
)

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$VenvPath = Join-Path $ScriptDir ".venv"
$AgentPath = Join-Path $ScriptDir "agent.py"
$Requirements = Join-Path $ScriptDir "requirements.txt"

# Проверка venv
if (-not (Test-Path -LiteralPath $VenvPath)) {
    Write-Host "[tiny-code] Creating virtual environment..." -ForegroundColor Yellow
    python -m venv $VenvPath
    if ($LASTEXITCODE -ne 0) {
        Write-Error "[tiny-code] Failed to create the virtual environment."
        exit 1
    }
    # Installing from requirements.txt keeps this list from drifting out of
    # sync with the declared dependencies, which is what happened before.
    & (Join-Path $VenvPath "Scripts\python") -m pip install --quiet -r $Requirements
    if ($LASTEXITCODE -ne 0) {
        Write-Error "[tiny-code] Failed to install dependencies."
        exit 1
    }
}

$Python = Join-Path $VenvPath "Scripts\python"
if (-not (Test-Path -LiteralPath $Python)) {
    $Python = Join-Path $VenvPath "Scripts\python.exe"
}

$argsList = @()

if (-not [string]::IsNullOrEmpty($workspace)) {
    $argsList += "--workspace", $workspace
}
if (-not [string]::IsNullOrEmpty($model)) {
    $argsList += "--model", $model
}
if (-not [string]::IsNullOrEmpty($permission)) {
    $argsList += "--permission", $permission
}

if (-not [string]::IsNullOrEmpty($prompt)) {
    $argsList += $prompt
    & $Python $AgentPath @argsList
} else {
    & $Python $AgentPath @argsList
}
