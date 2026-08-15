$ErrorActionPreference = 'Stop'

function Invoke-Block {
  param([string]$Name, [scriptblock]$Action)
  Write-Host "`n=== $Name ===" -ForegroundColor Cyan
  & $Action
  if ($LASTEXITCODE -ne 0) {
    throw "$Name failed with exit code $LASTEXITCODE"
  }
  Write-Host "$Name passed" -ForegroundColor Green
}

Invoke-Block '1/4 Node and MCP' {
  npm ci
  npm --prefix middleware/node ci
  npm --prefix middleware/node run build
  npm --prefix mcp-server ci
  npm --prefix mcp-server run build
  npm test
}

Invoke-Block '2/4 Soroban contracts' {
  cargo test --locked --manifest-path contracts/spending_limits/Cargo.toml
  cargo test --locked --manifest-path contracts/audit_log/Cargo.toml
}

Invoke-Block '3/4 Go backend' {
  $goPath = (Get-Command go -ErrorAction SilentlyContinue).Source
  if (!$goPath -and (Test-Path "$env:USERPROFILE\go-sdk\go\bin\go.exe")) {
    $goPath = "$env:USERPROFILE\go-sdk\go\bin\go.exe"
  }
  if (!$goPath) { throw 'Go toolchain not found.' }
  Push-Location backend
  try { & $goPath test ./... } finally { Pop-Location }
}

Invoke-Block '4/4 Python middleware' {
  python -m compileall -q middleware/python/x402_middleware
}

Write-Host "`nAll four CI blocks passed." -ForegroundColor Green
