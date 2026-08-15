const bytesToBinary = bytes => Array.from(bytes, byte => String.fromCharCode(byte)).join('');

export const decodeUTF8 = value => {
  if (typeof value !== 'string') throw new TypeError('expected string');
  return new TextEncoder().encode(value);
};

export const encodeUTF8 = value => {
  if (!(value instanceof Uint8Array)) throw new TypeError('expected Uint8Array');
  return new TextDecoder().decode(value);
};

export const encodeBase64 = value => btoa(bytesToBinary(value));

export const decodeBase64 = value => {
  if (typeof value !== 'string') throw new TypeError('expected string');
  const binary = atob(value);
  return Uint8Array.from(binary, character => character.charCodeAt(0));
};
