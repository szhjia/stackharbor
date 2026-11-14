import test from 'node:test';
import assert from 'node:assert/strict';
import {fingerprintInputs, macOSInstallerName, notarizationAuthorization, safeNotary, signingOnlyEnvironment, validateMacPublisher} from '../scripts/desktop-build.mjs';
test('public macOS installer names match the native release architecture convention', () => {
  assert.equal(macOSInstallerName('StackHarbor', '1.2.3', 'aarch64-apple-darwin'), 'StackHarbor_1.2.3_aarch64.dmg');
  assert.equal(macOSInstallerName('StackHarbor', '1.2.3', 'x86_64-apple-darwin'), 'StackHarbor_1.2.3_x64.dmg');
  assert.throws(() => macOSInstallerName('StackHarbor', '1.2.3', 'x86_64-unknown-linux-gnu'), /Unsupported macOS installer target/);
});
const identity = "Developer ID Application: Xi'an HengKe InfoTech Co.,Ltd (28CG45X4J3)";
test('existing explicit Keychain profile takes priority without exporting secrets', () => {
  assert.deepEqual(notarizationAuthorization({STACKHARBOR_NOTARY_KEYCHAIN_PROFILE: 'existing-profile', APPLE_ID:'ignored', APPLE_PASSWORD:'ignored'}), ['--keychain-profile','existing-profile']);
  assert.deepEqual(notarizationAuthorization({APPLE_NOTARIZATION_KEYCHAIN_PROFILE:'alias-profile'}), ['--keychain-profile','alias-profile']);
  assert.throws(() => notarizationAuthorization({}), /requires/);
});
test('existing legacy notarization authorization remains supported', () => {
  assert.deepEqual(notarizationAuthorization({APPLE_ID:'id',APPLE_PASSWORD:'password',APPLE_TEAM_ID:'28CG45X4J3'}), ['--apple-id','id','--password','password','--team-id','28CG45X4J3']);
  assert.deepEqual(notarizationAuthorization({APPLE_API_KEY:'key',APPLE_API_ISSUER:'issuer',APPLE_API_KEY_PATH:'/private/key.p8'}), ['--key','/private/key.p8','--key-id','key','--issuer','issuer']);
});
test('publisher/team/ad-hoc gates cannot be bypassed by a profile', () => {
  validateMacPublisher({APPLE_SIGNING_IDENTITY:identity}, `1) HASH "${identity}"`);
  assert.throws(() => validateMacPublisher({APPLE_SIGNING_IDENTITY:'-'}, identity));
  assert.throws(() => validateMacPublisher({APPLE_SIGNING_IDENTITY:'Other'}, 'Other'));
  assert.throws(() => validateMacPublisher({APPLE_SIGNING_IDENTITY:identity,APPLE_TEAM_ID:'WRONG'}, identity));
});
test('Tauri receives signing identity but no auto-notary credential material', () => {
  const input = {APPLE_SIGNING_IDENTITY:identity,STACKHARBOR_NOTARY_KEYCHAIN_PROFILE:'profile',APPLE_PASSWORD:'secret',APPLE_API_KEY_PATH:'/private/key',PATH:'/usr/bin'};
  const result = signingOnlyEnvironment(input);
  assert.deepEqual(result, {APPLE_SIGNING_IDENTITY:identity,PATH:'/usr/bin'});
  assert.equal(input.APPLE_PASSWORD,'secret');
});
test('notary invocation uses profile and failures never expose credential argv', () => {
  let args;
  assert.deepEqual(safeNotary(['history'], ['--keychain-profile','profile'], (command, actual) => {assert.equal(command,'xcrun');args=actual;return '{"history":[]}';}), {history:[]});
  assert.deepEqual(args, ['notarytool','history','--output-format','json','--keychain-profile','profile']);
  assert.throws(() => safeNotary(['history'], ['--password','sensitive'], () => {throw Error('argv --password sensitive');}), error => !String(error).includes('sensitive'));
});

test('source freeze fingerprint is stable across order and detects input edits', () => {
  const files = {'internal/engine/server.go':'first', 'desktop/src/main.rs':'second'};
  const read = path => files[path];
  const before = fingerprintInputs(Object.keys(files), read);
  assert.equal(before, fingerprintInputs(Object.keys(files).reverse(), read));
  files['internal/engine/server.go'] = 'changed';
  assert.notEqual(before, fingerprintInputs(Object.keys(files), read));
});

test('submit matches existing publisher direct-S3 transport and a bounded upload budget', () => {
  let captured;
  const value = safeNotary(['submit','/candidate.dmg'], ['--keychain-profile','profile'], (command,args,options) => { captured={command,args,options};return '{"id":"receipt"}'; });
  assert.equal(value.id,'receipt');
  assert.ok(captured.args.includes('--no-s3-acceleration'));
  assert.equal(captured.options.timeout,600000);
  assert.throws(() => safeNotary(['submit','/candidate.dmg'],['--keychain-profile','profile'],() => { const error=Error('argv secret');error.code='ETIMEDOUT';throw error; }), error => String(error).includes('process deadline exceeded') && !String(error).includes('secret'));
});
