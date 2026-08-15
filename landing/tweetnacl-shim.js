import naclModule from 'https://cdn.jsdelivr.net/npm/tweetnacl@1.0.3/+esm';

const nacl = naclModule?.default || naclModule;

export const randomBytes = (...args) => nacl.randomBytes(...args);
export const box = nacl.box;
export const secretbox = nacl.secretbox;
export const scalarMult = nacl.scalarMult;
export const sign = nacl.sign;
export const hash = nacl.hash;
export const verify = nacl.verify;
export const setPRNG = nacl.setPRNG;
export default nacl;
