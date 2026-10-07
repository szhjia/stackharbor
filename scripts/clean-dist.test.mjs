import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, symlinkSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { cleanDist } from './clean-dist.mjs'

function fixture(run) {
  const root = mkdtempSync(path.join(tmpdir(), 'stackharbor-clean-dist-'))
  const dist = path.join(root, 'dist')
  mkdirSync(dist)
  try { run(dist, root) } finally { rmSync(root, { recursive: true, force: true }) }
}

test('daily prune keeps the newest rollback and leaves releases and unrelated files alone', () => fixture((dist) => {
  for (const [name, time] of [['stackharbor.previous-aaaaaaaa', 1], ['stackharbor.previous-bbbbbbbb', 2], ['stackharbor.previous-cccccccc', 3]]) {
    const file = path.join(dist, name)
    writeFileSync(file, name)
    utimesSync(file, time, time)
  }
  writeFileSync(path.join(dist, 'stackharbor'), 'current')
  writeFileSync(path.join(dist, 'stackharbor_0.1.0_linux_arm64.tar.gz'), 'release')
  writeFileSync(path.join(dist, 'my-notes.txt'), 'keep')

  assert.deepEqual(cleanDist(dist, 'prune'), ['stackharbor.previous-aaaaaaaa', 'stackharbor.previous-bbbbbbbb'])
  assert.deepEqual(cleanDist(dist, 'prune'), [])
  assert.deepEqual(readdirSync(dist).sort(), ['my-notes.txt', 'stackharbor', 'stackharbor.previous-cccccccc', 'stackharbor_0.1.0_linux_arm64.tar.gz'])
}))

test('release prune removes old platform archives only after the new release exists', () => fixture((dist) => {
  writeFileSync(path.join(dist, 'stackharbor_0.1.0_linux_arm64.tar.gz'), 'old')
  for (const target of ['darwin_arm64', 'darwin_amd64', 'linux_arm64', 'linux_amd64']) {
    writeFileSync(path.join(dist, `stackharbor_0.5.0_${target}.tar.gz`), 'current')
  }
  writeFileSync(path.join(dist, 'stackharbor_0.5.0_linux_arm64.tar.gz.extra'), 'keep')
  writeFileSync(path.join(dist, 'SHA256SUMS'), 'manifest')
  assert.deepEqual(cleanDist(dist, 'release', '0.5.0'), ['stackharbor_0.1.0_linux_arm64.tar.gz'])
  assert.equal(readFileSync(path.join(dist, 'stackharbor_0.5.0_linux_arm64.tar.gz'), 'utf8'), 'current')
  assert.equal(readFileSync(path.join(dist, 'SHA256SUMS'), 'utf8'), 'manifest')
  assert.equal(readFileSync(path.join(dist, 'stackharbor_0.5.0_linux_arm64.tar.gz.extra'), 'utf8'), 'keep')
}))

test('release prune does not remove backups or archives while packaging is incomplete', () => fixture((dist) => {
  writeFileSync(path.join(dist, 'stackharbor.previous-aaaaaaaa'), 'old backup')
  writeFileSync(path.join(dist, 'stackharbor.previous-bbbbbbbb'), 'new backup')
  writeFileSync(path.join(dist, 'stackharbor_0.1.0_linux_arm64.tar.gz'), 'old release')
  writeFileSync(path.join(dist, 'stackharbor_0.5.0_linux_arm64.tar.gz'), 'partial release')
  assert.throws(() => cleanDist(dist, 'release', '0.5.0'), /release is complete/)
  assert.equal(readdirSync(dist).length, 4)
}))

test('full clean clears dist but refuses a symlinked dist directory', () => fixture((dist, root) => {
  writeFileSync(path.join(dist, 'custom-file'), 'custom')
  cleanDist(dist, 'all')
  assert.throws(() => readdirSync(dist), { code: 'ENOENT' })
  const outside = path.join(root, 'outside')
  mkdirSync(outside)
  writeFileSync(path.join(outside, 'keep'), 'keep')
  symlinkSync(outside, dist)
  assert.throws(() => cleanDist(dist, 'all'), /symlink/)
  assert.equal(readFileSync(path.join(outside, 'keep'), 'utf8'), 'keep')
}))
