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

# Проверка venv
if (-not (Test-Path -LiteralPath $VenvPath)) {
    Write-Host "[tiny-code] Creating virtual environment..." -ForegroundColor Yellow
    python -m venv $VenvPath
    & (Join-Path $VenvPath "Scripts\python") -m pip install --quiet openai json_repair duckduckgo_search requests
}

$Python = Join-Path $VenvPath "Scripts\python"

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
