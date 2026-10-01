import assert from 'node:assert/strict';
import { appendFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

const releasePath = '.github/workflows/release.yaml';
const git = (...args) => execFileSync('git', args, { encoding: 'utf8' }).trim();

// CODEOWNERS supports neither negation nor character ranges. Reject constructs
// this matcher cannot resolve, rather than accidentally granting release access.
export function matches(pattern, path) {
  assert(!/[!\[\]\\]/.test(pattern), 'Unsupported CODEOWNERS pattern');
  const rooted = pattern.startsWith('/');
  pattern = pattern.replace(/^\//, '');
  let expression = '';
  for (let i = 0; i < pattern.length; i++) {
    const char = pattern[i];
    if (char === '*' && pattern[i + 1] === '*') {
      i++;
      if (pattern[i + 1] === '/') { i++; expression += '(?:.*/)?'; }
      else expression += '.*';
    } else if (char === '*') expression += '[^/]*';
    else if (char === '?') expression += '[^/]';
    else expression += char.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }
  const prefix = rooted || pattern.replace(/\/$/, '').includes('/') ? '^' : '(?:^|/)';
  const suffix = pattern.endsWith('/') ? '.*$' : /[*?]/.test(pattern) ? '$' : '(?:/.*)?$';
  return new RegExp(prefix + expression + suffix).test(path);
}

export function ownersFor(text, path = releasePath) {
  let owners = [];
  for (const line of text.split(/\r?\n/)) {
    const fields = line.split('#')[0].trim().split(/\s+/);
    if (!fields[0]) continue;
    if (matches(fields[0], path)) owners = fields.slice(1);
  }
  assert(owners.length, 'Release workflow has no effective CODEOWNERS');
  assert(owners.every(owner => /^@[a-z\d](?:[a-z\d-]*[a-z\d])?$/i.test(owner)),
    'Release CODEOWNERS must be named GitHub users; teams/emails need a separate membership integration');
  return owners.map(owner => owner.slice(1).toLowerCase());
}

export function authorise(text, actor, triggeringActor) {
  const owners = ownersFor(text);
  for (const user of [actor, triggeringActor]) {
    assert(user && owners.includes(user.toLowerCase()), `${user || 'Missing actor'} is not a release CODEOWNER`);
  }
}

export function nextTag(tags, bump) {
  assert(['patch', 'minor', 'major'].includes(bump), 'Invalid version increment');
  const versions = tags.filter(tag => /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(tag))
    .map(tag => tag.slice(1).split('.').map(BigInt));
  versions.sort((a, b) => {
    for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] > b[i] ? -1 : 1;
    return 0;
  });
  const version = versions[0] || [0n, 0n, 0n];
  const index = { major: 0, minor: 1, patch: 2 }[bump];
  version[index]++;
  for (let i = index + 1; i < 3; i++) version[i] = 0n;
  return `v${version.join('.')}`;
}

export function selectTag(tags, records, bump, runID, sha) {
  assert(/^\d+$/.test(runID), 'Invalid workflow run ID');
  const prior = records.filter(record => record.subject === `Release ${record.tag} (run ${runID})`);
  assert(prior.length <= 1, 'Multiple tags belong to this workflow run');
  if (prior.length) {
    assert(/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(prior[0].tag), 'Invalid prior tag');
    assert(prior[0].sha === sha, 'Prior tag points to another commit');
    return prior[0].tag;
  }
  return nextTag(tags, bump);
}

export function validateContext(event, ref, defaultBranch) {
  assert(/^[a-zA-Z0-9._/-]+$/.test(defaultBranch), 'Invalid default branch');
  assert(event === 'workflow_dispatch' || event === 'push', 'Unsupported release event');
  if (event === 'workflow_dispatch') assert(ref === `refs/heads/${defaultBranch}`, 'Dispatch releases from the default branch only');
  else assert(/^refs\/tags\/v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(ref), 'Only stable semantic version tags can release');
}

async function api(path) {
  const response = await fetch(`https://api.github.com/repos/${process.env.GITHUB_REPOSITORY}/${path}`, {
    headers: { Authorization: `Bearer ${process.env.GH_TOKEN}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' },
    redirect: 'error', signal: AbortSignal.timeout(30000),
  });
  assert(response.ok, `GitHub API check failed (${response.status})`);
  return response.json();
}

export function requireGreenCI(runs, sha, branch) {
  const eligible = runs.filter(run => run.head_sha === sha && run.head_branch === branch && run.event === 'push');
  eligible.sort((a, b) => b.id - a.id);
  assert(eligible.length && eligible[0].status === 'completed' && eligible[0].conclusion === 'success',
    'Latest default-branch CI for this exact commit must finish successfully before release');
  return eligible[0];
}

async function main() {
  const env = process.env;
  const branch = env.DEFAULT_BRANCH;
  validateContext(env.GITHUB_EVENT_NAME, env.GITHUB_REF, branch);
  const source = git('rev-parse', 'HEAD');
  const head = await api(`git/ref/heads/${encodeURIComponent(branch)}`);
  const policy = git('show', `origin/${branch}:.github/CODEOWNERS`);
  authorise(policy, env.GITHUB_ACTOR, env.GITHUB_TRIGGERING_ACTOR);
  for (const actor of new Set([env.GITHUB_ACTOR, env.GITHUB_TRIGGERING_ACTOR])) {
    const access = await api(`collaborators/${encodeURIComponent(actor)}/permission`);
    assert(['admin', 'maintain', 'write'].includes(access.permission), 'Release CODEOWNER must retain repository write access');
  }
  if (env.GITHUB_EVENT_NAME === 'workflow_dispatch') {
    assert(source === env.GITHUB_SHA && head.object.sha === source, 'Default branch moved; dispatch a fresh release run');
  } else {
    git('merge-base', '--is-ancestor', source, `origin/${branch}`);
  }
  if (env.RELEASE_PHASE === 'check-head') {
    assert(head.object.sha === source, 'Default branch moved during validation; tag was not created');
    return;
  }
  const ci = await api(`actions/workflows/ci.yaml/runs?head_sha=${source}&event=push&per_page=100`);
  const run = requireGreenCI(ci.workflow_runs, source, branch);
  const tags = git('tag', '--list').split('\n').filter(Boolean);
  const records = tags.map(tag => ({ tag,
    subject: git('for-each-ref', '--format=%(contents:subject)', `refs/tags/${tag}`),
    sha: git('rev-parse', `${tag}^{commit}`),
  }));
  const tag = env.GITHUB_EVENT_NAME === 'workflow_dispatch'
    ? selectTag(tags, records, env.RELEASE_BUMP, env.GITHUB_RUN_ID, source)
    : env.GITHUB_REF.slice('refs/tags/'.length);
  const name = `https://github.com/${env.GITHUB_REPOSITORY}/${releasePath}@${env.GITHUB_REF}`;
  appendFileSync(env.GITHUB_OUTPUT, `tag=${tag}\nsha=${source}\nidentity=${name}\n`);
  appendFileSync(env.GITHUB_STEP_SUMMARY, `Release **${tag}** from \`${source}\`, authorised for @${env.GITHUB_ACTOR}. [Successful CI](${run.html_url}).\n\nCosign identity: \`${name}\`.\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
