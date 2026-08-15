import freighterModule, { freighterApi } from 'https://cdn.jsdelivr.net/npm/@stellar/freighter-api@6.0.0/+esm';

const freighter = freighterApi || freighterModule?.freighterApi || freighterModule;

export const getAddress = (...args) => freighter.getAddress(...args);
export const getNetwork = (...args) => freighter.getNetwork(...args);
export const isConnected = (...args) => freighter.isConnected(...args);
export const requestAccess = (...args) => freighter.requestAccess(...args);
export const signAuthEntry = (...args) => freighter.signAuthEntry(...args);
export const signMessage = (...args) => freighter.signMessage(...args);
export const signTransaction = (...args) => freighter.signTransaction(...args);
export default freighter;
