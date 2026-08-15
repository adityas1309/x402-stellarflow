import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const html = await readFile(join(root, 'landing', 'index.html'), 'utf8');
const freighterShim = await readFile(join(root, 'landing', 'freighter-api-shim.js'), 'utf8');
const tweetnaclShim = await readFile(join(root, 'landing', 'tweetnacl-util-shim.js'), 'utf8');
const tweetnaclCoreShim = await readFile(join(root, 'landing', 'tweetnacl-shim.js'), 'utf8');

test('landing page exposes the live product flow', () => {
  assert.match(html, /stellar:testnet/);
  assert.match(html, /HTTP 402/);
  assert.match(html, /Connect a Stellar wallet/);
  assert.match(html, /Pay 0\.05 USDC and run/);
  assert.match(html, /Transaction/);
  assert.match(html, /stellar\.expert\/explorer\/testnet/);
});

test('landing page includes responsive layout and loading/error states', () => {
  assert.match(html, /@media \(max-width: 800px\)/);
  assert.match(html, /requesting|checking|approve/i);
  assert.match(html, /Payment not completed|error/i);
  assert.match(html, /disabled/);
});

test('wallet connection opens the multi-wallet selector with a compatible Freighter API', () => {
  assert.match(html, /stellar-wallets-kit@2\.5\.0\/sdk\?bundle&deps=@stellar\/freighter-api@6\.0\.0/);
  assert.match(html, /stellar-wallets-kit@2\.5\.0\/modules\/utils\?deps=@stellar\/freighter-api@6\.0\.0/);
  assert.match(html, /walletKit\.authModal\(\{ showInstallLabel: true \}\)/);
  assert.match(html, /freighter-api@6\.0\.0\/es2022\/freighter-api\.mjs/);
  assert.match(html, /https:\/\/esm\.sh\/@stellar\/freighter-api@6\.0\.0\/es2022\/freighter-api\.mjs/);
  assert.match(freighterShim, /export const getAddress/);
  assert.match(freighterShim, /export const signAuthEntry/);
  assert.match(html, /https:\/\/esm\.sh\/tweetnacl-util@\^0\.15\.1\?target=es2022/);
  assert.match(tweetnaclShim, /export const encodeUTF8/);
  assert.match(tweetnaclShim, /export const decodeBase64/);
  assert.match(html, /https:\/\/esm\.sh\/tweetnacl@\^1\.0\.3\?target=es2022/);
  assert.match(tweetnaclCoreShim, /export const randomBytes/);
  assert.match(tweetnaclCoreShim, /export const box/);
});

test('landing page exposes agent and contract evidence', () => {
  assert.match(html, /MCP/);
  assert.match(html, /spending-limit contract/i);
  assert.match(html, /CBRE5KJZRMX6VOPPO6PZOVLMAKIFPB6SERENFHDHULRKG5NGVQ6ZTZ4F/);
});
