import { test } from 'node:test';
import assert from 'node:assert/strict';
import { authorise, ownersFor, nextTag, selectTag, validateContext, requireGreenCI } from './release.mjs';

test('last matching owners control release permission; both actors must qualify', () => {
  const policy = '* @general\n/.github/ @platform\n/.github/workflows/release.yaml @davidcollom\n';
  assert.deepEqual(ownersFor(policy), ['davidcollom']);
  authorise(policy, 'DavidCollom', 'davidcollom');
  assert.throws(() => authorise(policy, 'general', 'general'));
  assert.throws(() => authorise(policy, 'davidcollom', 'outsider'));
  assert.throws(() => authorise(policy, undefined, 'davidcollom'));
});
test('basename and recursive patterns follow path boundaries', () => {
  assert.deepEqual(ownersFor('* @base\n*.yaml @yaml'), ['yaml']);
  assert.deepEqual(ownersFor('* @base\n/.github/**/release.yaml @release'), ['release']);
  assert.deepEqual(ownersFor('* @base\n/.github/* @other'), ['base']);
  assert.deepEqual(ownersFor('* @base\nworkflows/ @other'), ['other']);
});
test('missing, cleared and unsupported ownership fails closed', () => {
  for (const policy of ['', 'README.md @someone', '* @org/team', '* user@example.com', '* @owner\n/.github/workflows/release.yaml', '[abc] @owner']) {
    assert.throws(() => ownersFor(policy));
  }
});
test('version bumps sort numerically, ignore prereleases and reset components', () => {
  const tags = ['v0.9.9', 'v0.10.2', 'v1.0.0-rc.1', 'other', 'v01.99.0'];
  assert.equal(nextTag(tags, 'patch'), 'v0.10.3');
  assert.equal(nextTag(tags, 'minor'), 'v0.11.0');
  assert.equal(nextTag(tags, 'major'), 'v1.0.0');
  assert.equal(nextTag([], 'minor'), 'v0.1.0');
  assert.equal(nextTag([], 'patch'), 'v0.0.1');
  assert.equal(nextTag([], 'major'), 'v1.0.0');
  assert.throws(() => nextTag(tags, 'invalid'));
});
test('manual runs require the default branch; tag runs require stable versions', () => {
  validateContext('workflow_dispatch', 'refs/heads/main', 'main');
  validateContext('push', 'refs/tags/v0.1.0', 'main');
  assert.throws(() => validateContext('workflow_dispatch', 'refs/heads/feature', 'main'));
  assert.throws(() => validateContext('workflow_dispatch', 'refs/tags/v0.1.0', 'main'));
  assert.throws(() => validateContext('push', 'refs/tags/v0.1.0-rc.1', 'main'));
  assert.throws(() => validateContext('pull_request', 'refs/heads/main', 'main'));
});
test('CI must be successful for the exact default-branch commit, including latest attempts', () => {
  const good = { id: 1, head_sha: 'abc', head_branch: 'main', event: 'push', status: 'completed', conclusion: 'success' };
  assert.equal(requireGreenCI([good], 'abc', 'main'), good);
  for (const change of [{ head_sha: 'old' }, { head_branch: 'feature' }, { event: 'pull_request' }, { status: 'in_progress' }, { conclusion: 'failure' }]) {
    assert.throws(() => requireGreenCI([{ ...good, ...change }], 'abc', 'main'));
  }
  assert.throws(() => requireGreenCI([good, { ...good, id: 2, conclusion: 'failure' }], 'abc', 'main'));
});

test('workflow reruns resume only their own immutable tag', () => {
  const tag = { tag: 'v0.1.0', subject: 'Release v0.1.0 (run 123)', sha: 'abc' };
  assert.equal(selectTag(['v0.1.0'], [tag], 'minor', '123', 'abc'), 'v0.1.0');
  assert.equal(selectTag(['v0.1.0'], [tag], 'minor', '456', 'abc'), 'v0.2.0');
  assert.throws(() => selectTag(['v0.1.0'], [tag], 'minor', '123', 'other'));
  assert.throws(() => selectTag(['v0.1.0'], [tag, tag], 'minor', '123', 'abc'));
});
