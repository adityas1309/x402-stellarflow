import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const html = await readFile(join(root, 'landing', 'index.html'), 'utf8');

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

test('landing page exposes agent and contract evidence', () => {
  assert.match(html, /MCP/);
  assert.match(html, /spending-limit contract/i);
  assert.match(html, /CBRE5KJZRMX6VOPPO6PZOVLMAKIFPB6SERENFHDHULRKG5NGVQ6ZTZ4F/);
});
