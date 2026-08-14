$ErrorActionPreference = "Stop"

# Run this from PowerShell while screen-recording the terminal.
# It performs one real 0.05 USDC Stellar testnet payment.
$root = Split-Path -Parent $PSScriptRoot
$mcp = Join-Path $root "mcp-server"
$base = "https://x402-mcp-stellar-template-main.vercel.app"
$treasury = "GBLDFWELHTPY4SIW6BNHDPFAYLH3NR5N2HK5VTK5GPAUMK5OESE4SYR7"
$walletPath = Join-Path $root ".agent-testnet.json"

function Step([string]$title) {
  Write-Host ""
  Write-Host ("=" * 72) -ForegroundColor DarkGray
  Write-Host $title -ForegroundColor Cyan
  Write-Host ("=" * 72) -ForegroundColor DarkGray
}

Step "1. Discover the paid API"
Write-Host "curl.exe $base/api/catalog" -ForegroundColor Yellow
curl.exe "$base/api/catalog"

Step "2. Trigger the real HTTP 402 challenge"
$payload = '{"topic":"stellar blockchain"}'
Write-Host "Invoke-WebRequest -Method POST $base/api/x402/sentiment -ContentType application/json -Body $payload" -ForegroundColor Yellow
try {
  $challenge = Invoke-WebRequest -UseBasicParsing -Uri "$base/api/x402/sentiment" -Method Post -ContentType "application/json" -Body $payload
  Write-Host ("Unexpected HTTP " + $challenge.StatusCode) -ForegroundColor Red
} catch {
  $status = $_.Exception.Response.StatusCode.value__
  Write-Host ("HTTP " + $status + " Payment Required") -ForegroundColor Green
  Write-Host "The API is waiting for a valid X-Payment header."
}

Step "3. Build the MCP agent server"
Set-Location $mcp
Write-Host "npm run build" -ForegroundColor Yellow
npm run build

if (!(Test-Path $walletPath)) {
  throw "Missing $walletPath. Create a testnet wallet first with: npx x402-mcp init"
}

$wallet = Get-Content $walletPath -Raw | ConvertFrom-Json
if (!$wallet.address -or !$wallet.secret) {
  throw "The testnet wallet file is missing address or secret."
}

Step "4. Configure the live Stellar testnet agent"
$env:STELLARFLOW_BASE_URL = $base
$env:STELLARFLOW_NETWORK = "stellar:testnet"
$env:STELLARFLOW_DRY_RUN = "false"
$env:STELLARFLOW_AGENT_ADDRESS = $wallet.address
$env:STELLARFLOW_AGENT_SECRET = $wallet.secret
$env:LIVE = "true"
Write-Host "Backend: $env:STELLARFLOW_BASE_URL"
Write-Host "Network: $env:STELLARFLOW_NETWORK"
Write-Host ("Agent: " + $wallet.address)
Write-Host "Secret: loaded from local wallet file, never printed"

Step "5. Ask the MCP agent for live analysis"
Write-Host "MCP agent requests stellarflow_sentiment for: stellar blockchain" -ForegroundColor Yellow
$beforeTransactions = Invoke-RestMethod "https://horizon-testnet.stellar.org/accounts/$treasury/transactions?limit=20&order=desc"
$beforeHashes = @($beforeTransactions._embedded.records | ForEach-Object { $_.hash })
 $passed = $false
$attemptLog = Join-Path $env:TEMP "stellarflow-mcp-attempt.log"
for ($attempt = 1; $attempt -le 3; $attempt++) {
  $previousErrorAction = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  cmd.exe /c "node scripts\smoke-test.mjs > `"$attemptLog`" 2>&1"
  $attemptExitCode = $LASTEXITCODE
  $ErrorActionPreference = $previousErrorAction
  if ($attemptExitCode -eq 0) {
    $passed = $true
    Get-Content $attemptLog
    break
  }
  if ($attempt -lt 3) {
    Write-Host "Transient testnet auth timing error. Retrying in 8 seconds..." -ForegroundColor Yellow
    Start-Sleep -Seconds 8
  }
}
if (!$passed) {
    throw "The live agent payment failed three times. No successful payment was recorded."
}
Remove-Item $attemptLog -Force -ErrorAction SilentlyContinue

Step "6. Verify the latest treasury settlement"
$latest = $null
for ($poll = 1; $poll -le 20; $poll++) {
  $transactions = Invoke-RestMethod "https://horizon-testnet.stellar.org/accounts/$treasury/transactions?limit=10&order=desc"
  $latest = $transactions._embedded.records |
    Where-Object { $_.successful -and $_.hash -notin $beforeHashes } |
    Select-Object -First 1
  if ($latest) { break }
  Start-Sleep -Seconds 2
}
if ($latest) {
  Write-Host ("Transaction: " + $latest.hash) -ForegroundColor Green
  Write-Host ("Explorer: https://stellar.expert/explorer/testnet/tx/" + $latest.hash) -ForegroundColor Green
} else {
  throw "The API returned a paid result, but no fresh Stellar settlement hash was indexed. Do not record this run as successful."
}

Step "Live payment flow complete"
Write-Host "The agent discovered HTTP 402, signed 0.05 USDC, received the paid result, and confirmed the Stellar transaction above."
