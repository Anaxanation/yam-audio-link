const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

const keyPath = path.join(__dirname, 'catalog-private.pem');
if (!fs.existsSync(keyPath)) {
  const pair = crypto.generateKeyPairSync('ed25519');
  fs.writeFileSync(keyPath, pair.privateKey.export({type: 'pkcs8', format: 'pem'}), {mode: 0o600, flag: 'wx'});
}
const privateKey = crypto.createPrivateKey(fs.readFileSync(keyPath));
const publicKey = crypto.createPublicKey(privateKey);
const payload = fs.readFileSync(path.join(__dirname, 'catalog-payload.json'));
const signature = crypto.sign(null, payload, privateKey);
if (!crypto.verify(null, payload, publicKey, signature)) throw new Error('Signature verification failed');
const publicKeyText = publicKey.export({format: 'jwk'}).x;
const rawKey = Buffer.from(publicKeyText, 'base64url').toString('base64');
const docs = path.join(__dirname, 'publish/docs');
fs.writeFileSync(path.join(docs, 'catalog/v2.json'), JSON.stringify({
  payload: payload.toString('base64'), signature: signature.toString('base64'), public_key: rawKey,
}, null, 2) + '\n');
fs.writeFileSync(path.join(docs, 'public-key.txt'), rawKey + '\n');
