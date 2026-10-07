import { lstatSync, readdirSync, rmSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const backupName = /^stackharbor\.previous-[a-f0-9]{8,64}$/
const archiveName = /^stackharbor_(\d+\.\d+\.\d+)_(darwin|linux)_(arm64|amd64)\.tar\.gz$/
const targets = ['darwin_arm64', 'darwin_amd64', 'linux_arm64', 'linux_amd64']

export function cleanDist(dist, mode, version) {
  if (!['prune', 'release', 'all'].includes(mode)) throw new Error('Expected prune, release, or all')
  if (mode === 'release' && !/^\d+\.\d+\.\d+$/.test(version ?? '')) throw new Error('Expected a release version')
  let directory
  try { directory = lstatSync(dist) } catch (error) {
    if (error.code === 'ENOENT') return []
    throw error
  }
  if (directory.isSymbolicLink()) throw new Error(`Refusing to clean symlinked dist: ${dist}`)
  if (!directory.isDirectory()) throw new Error(`Not a directory: ${dist}`)

  if (mode === 'all') {
    rmSync(dist, { recursive: true })
    return ['dist/']
  }

  const files = readdirSync(dist, { withFileTypes: true }).filter((entry) => entry.isFile())
  const backups = files.filter((entry) => backupName.test(entry.name))
    .map((entry) => ({ name: entry.name, time: statSync(path.join(dist, entry.name)).mtimeMs }))
    .sort((a, b) => b.time - a.time || b.name.localeCompare(a.name))
  const removed = backups.slice(1).map((entry) => entry.name)

  if (mode === 'release') {
    for (const target of targets) {
      const name = `stackharbor_${version}_${target}.tar.gz`
      if (!files.some((entry) => entry.name === name)) throw new Error(`Cannot prune before release is complete: ${name} is missing`)
    }
    if (!files.some((entry) => entry.name === 'SHA256SUMS')) throw new Error('Cannot prune before release is complete: SHA256SUMS is missing')
    for (const entry of files) {
      const match = archiveName.exec(entry.name)
      if (match && match[1] !== version) removed.push(entry.name)
    }
  }

  removed.sort()
  for (const name of removed) rmSync(path.join(dist, name))
  return removed
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [mode, version, ...extra] = process.argv.slice(2)
  if (extra.length || !['prune', 'release', 'all'].includes(mode) || (mode === 'release' ? !version : !!version)) {
    console.error('Usage: node scripts/clean-dist.mjs prune|release <version>|all')
    process.exit(2)
  }
  const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
  const removed = cleanDist(dist, mode, version)
  console.log(removed.length ? `Removed ${removed.length} dist item(s): ${removed.join(', ')}` : 'dist is already clean')
}
