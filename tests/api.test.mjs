import test, { after, before } from 'node:test';
import assert from 'node:assert/strict';
import app from '../api/index.js';

let server;
let baseUrl;

before(async () => {
  server = app.listen(0);
  await new Promise((resolve) => server.once('listening', resolve));
  baseUrl = `http://127.0.0.1:${server.address().port}`;
});

after(async () => {
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
});

test('catalog advertises a testnet USDC service', async () => {
  const response = await fetch(`${baseUrl}/api/catalog`);
  const body = await response.json();
  assert.equal(response.status, 200);
  assert.equal(body.catalog[0].network, 'stellar:testnet');
  assert.equal(body.catalog[0].asset_code, 'USDC');
  assert.equal(body.catalog[0].price_usdc, 0.05);
});

test('paid endpoint rejects requests without an X-Payment header', async () => {
  const response = await fetch(`${baseUrl}/api/x402/sentiment`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ topic: 'stellar' }),
  });
  const body = await response.json();
  assert.equal(response.status, 402);
  assert.equal(body.x402Version, 2);
  assert.equal(body.accepts[0].network, 'stellar:testnet');
  assert.equal(body.accepts[0].extra.code, 'USDC');
});
