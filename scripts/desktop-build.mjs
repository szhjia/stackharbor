import { execFileSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../", import.meta.url));
export const expectedTeam = "28CG45X4J3";
export const expectedPublisher = "Xi'an HengKe InfoTech Co.,Ltd";

export function macOSInstallerName(productName, version, target) {
  // Match Tauri's DMG architecture names and the public release validator.
  const architecture = {"aarch64-apple-darwin": "aarch64", "x86_64-apple-darwin": "x64"}[target];
  if (!architecture) throw Error(`Unsupported macOS installer target: ${target}`);
  return `${productName}_${version}_${architecture}.dmg`;
}

export function notarizationAuthorization(env) {
  const profile = env.STACKHARBOR_NOTARY_KEYCHAIN_PROFILE || env.APPLE_NOTARIZATION_KEYCHAIN_PROFILE;
  if (profile) return ["--keychain-profile", profile];
  if (env.APPLE_API_KEY && env.APPLE_API_ISSUER && env.APPLE_API_KEY_PATH) return ["--key", env.APPLE_API_KEY_PATH, "--key-id", env.APPLE_API_KEY, "--issuer", env.APPLE_API_ISSUER];
  if (env.APPLE_ID && env.APPLE_PASSWORD && env.APPLE_TEAM_ID) return ["--apple-id", env.APPLE_ID, "--password", env.APPLE_PASSWORD, "--team-id", env.APPLE_TEAM_ID];
  throw Error("Public macOS distribution requires an existing notarization Keychain profile or authorized notarization credentials");
}
export function validateMacPublisher(env, identities) {
  if (!env.APPLE_SIGNING_IDENTITY || env.APPLE_SIGNING_IDENTITY === "-") throw Error("Public macOS distribution requires the existing Developer ID signing identity");
  const line = identities.split("\n").find(line => line.includes(env.APPLE_SIGNING_IDENTITY));
  if (!line || !line.includes(`Developer ID Application: ${expectedPublisher} (${expectedTeam})`)) throw Error("Public distribution must use the existing StackHarbor/Infinite Remote Developer ID publisher");
  if (env.APPLE_TEAM_ID && env.APPLE_TEAM_ID !== expectedTeam) throw Error("Notarization team does not match the existing Developer ID team");
}
export function signingOnlyEnvironment(env) {
  const signing = {...env};
  // Tauri signs the app; the explicit notarization pipeline below submits its
  // exact signed bytes. Never extract Keychain credentials for Tauri's env.
  for (const key of ["APPLE_ID", "APPLE_PASSWORD", "APPLE_TEAM_ID", "APPLE_API_KEY", "APPLE_API_ISSUER", "APPLE_API_KEY_PATH", "STACKHARBOR_NOTARY_KEYCHAIN_PROFILE", "APPLE_NOTARIZATION_KEYCHAIN_PROFILE"]) delete signing[key];
  return signing;
}
export function safeNotary(args, authorization, execute = execFileSync) {
  const operation = ["submit", "history", "info", "wait", "log"].includes(args[0]) ? args[0] : "request";
  const transport = operation === "submit" && !args.includes("--no-s3-acceleration") ? ["--no-s3-acceleration"] : [];
  try {
    // Match the existing publisher's working notarization transport. A submit
    // can create a receipt before upload finishes, so never blindly retry it.
    return JSON.parse(execute("xcrun", ["notarytool", ...args, ...transport, "--output-format", "json", ...authorization], {encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: operation === "submit" ? 600000 : 120000}));
  } catch (error) {
    const diagnostic = error?.code === "ETIMEDOUT" ? "process deadline exceeded" : `exit ${Number.isInteger(error?.status) ? error.status : "unknown"}`;
    // execFileSync errors contain argv, including passwords for the legacy
    // auth path. Do not attach the original error or print stderr/credentials.
    throw Error(`Apple notarization ${operation} failed (${diagnostic}); verify Keychain/service access and inspect history before resubmitting`);
  }
}

export function fingerprintInputs(paths, read = path => readFileSync(join(root, path))) {
  const digest = createHash("sha256");
  for (const path of [...new Set(paths)].sort()) { digest.update(path); digest.update("\0"); digest.update(read(path)); digest.update("\0"); }
  return digest.digest("hex");
}
function sourceFingerprint() {
  const paths = execFileSync("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard"], {cwd: root, encoding: "utf8"}).split("\0").filter(path => /^(cmd\/|internal\/|desktop\/|web\/|skills\/|licenses\/|scripts\/(desktop|web)|go\.(mod|sum)$|LICENSE$|THIRD_PARTY_NOTICES$)/.test(path));
  // The staged resources are immutable build inputs too. The Go binary is
  // fingerprinted separately because generated/binaries paths are ignored.
  const target = readFileSync(join(root, "desktop/generated/target"), "utf8").trim();
  paths.push(`desktop/binaries/stackharbor-${target}`);
  return fingerprintInputs(paths);
}

export async function buildDesktop() {
  const run = (command, args, cwd = root, env = process.env) => execFileSync(command, args, {cwd, env, stdio: "inherit"});
  const release = process.env.STACKHARBOR_DESKTOP_DISTRIBUTION === "release";
  const candidate = process.env.STACKHARBOR_DESKTOP_CANDIDATE === "1";
  const env = {...process.env};
  let authorization = [];
  if (process.platform === "darwin") {
    if (release) {
      validateMacPublisher(env, execFileSync("security", ["find-identity", "-v", "-p", "codesigning"], {encoding: "utf8"}));
      authorization = notarizationAuthorization(env);
      // Read-only verification. Never create/update a Keychain profile, API key
      // or developer account, and never export stored credentials.
      safeNotary(["history"], authorization);
    } else env.APPLE_SIGNING_IDENTITY = "-";
  }
  run("sh", ["scripts/web-build.sh"]);
  run(process.execPath, ["scripts/desktop-prepare.mjs"]);
  run("npm", ["ci"], join(root, "desktop"));
  run("python3", ["scripts/desktop-licenses.py"]);
  const target = readFileSync(join(root, "desktop/generated/target"), "utf8").trim();
  const metadata = JSON.parse(readFileSync(join(root, "desktop/generated/build.json"), "utf8"));
  const fingerprint = sourceFingerprint();
  metadata.source_fingerprint = fingerprint;
  writeFileSync(join(root, "desktop/generated/build.json"), JSON.stringify(metadata, null, 2) + "\n");
  const distribution = release ? candidate ? "release-candidate" : "release" : "internal";
  const output = join(root, "dist/desktop", `${metadata.version}-${target}-${distribution}`);
  mkdirSync(output, {recursive: true});
  const bundle = join(root, "desktop/target", target, "release/bundle");
  const config = JSON.parse(readFileSync(join(root, "desktop/tauri.conf.json"), "utf8"));
  const installerPrefix = config.productName + `_${metadata.version}_`;
  const evidence = {team: expectedTeam, publisher: expectedPublisher, target, candidate, source_dirty: metadata.source_dirty, steps: [], submissions: []};
  const record = step => { evidence.steps.push(step); writeFileSync(join(output, "notarization.json"), JSON.stringify(evidence, null, 2) + "\n"); };
  const notarize = async (archive, kind) => {
    // Optional orchestration barrier: a local performance/review coordinator
    // may let compilation finish while retaining an explicit upload hold.
    const gate = process.env.STACKHARBOR_NOTARY_UPLOAD_GATE;
    if (gate) {
      console.log(`Apple upload held for ${kind}; awaiting the local source-specific acceptance gate`);
      while (!existsSync(gate)) await new Promise(resolve => setTimeout(resolve, 5000));
      const approval = JSON.parse(readFileSync(gate, "utf8"));
      if (approval.source_fingerprint !== fingerprint) throw Error("The upload acceptance gate does not match these source inputs; no upload was made");
    }
    if (sourceFingerprint() !== fingerprint) throw Error("Source inputs changed during the candidate build; no further Apple upload was made. Rebuild after the source is frozen.");
    const submitted = safeNotary(["submit", archive], authorization);
    if (!submitted.id) throw Error("Apple notarization did not return a submission ID");
    const job = {kind, id: submitted.id, sha256: createHash("sha256").update(readFileSync(archive)).digest("hex"), status: submitted.status || "Submitted"};
    evidence.submissions.push(job); record(`submitted-${kind}`);
    const deadline = Date.now() + 60 * 60 * 1000;
    while (true) {
      const status = safeNotary(["info", job.id], authorization);
      job.status = status.status; record(`checked-${kind}`);
      console.log(`Apple notarization ${kind}: ${job.id} ${job.status}`);
      if (job.status === "Accepted") return;
      if (job.status !== "In Progress") throw Error(`Apple notarization ${kind} was not accepted; distribution stopped (submission ${job.id})`);
      if (Date.now() >= deadline) throw Error(`Apple notarization remains pending; resume verification of submission ${job.id} before distributing`);
      await new Promise(resolve => setTimeout(resolve, 15000));
    }
  };
  // Build the app once. Re-bundling it after stapling can replace the exact
  // notarized bundle, so the final DMG is made from this stapled app directly.
  const buildArgs = ["run", "tauri", "--", "build", "--ci", "--target", target];
  if (release && process.platform === "darwin") buildArgs.push("--bundles", "app");
  run("npm", buildArgs, join(root, "desktop"), release && process.platform === "darwin" ? signingOnlyEnvironment(env) : env);
  let installers = [];
  if (process.platform === "darwin") {
    const application = join(bundle, "macos/StackHarbor.app");
    run("/usr/bin/codesign", ["--verify", "--deep", "--strict", application]);
    run("/usr/bin/codesign", ["--verify", "--strict", join(application, "Contents/MacOS/stackharbor")]);
    if (release) {
      record("sign-app");
      const temporary = mkdtempSync(join(tmpdir(), "stackharbor-notary-"));
      try {
        const archive = join(temporary, "StackHarbor.app.zip");
        run("/usr/bin/ditto", ["-c", "-k", "--keepParent", application, archive]);
        await notarize(archive, "app"); record("notarize-app-zip");
        run("xcrun", ["stapler", "staple", application]); record("staple-app");
        run("/usr/bin/codesign", ["--verify", "--deep", "--strict", application]);
        run("xcrun", ["stapler", "validate", application]);
        run("/usr/sbin/spctl", ["--assess", "--type", "execute", "--verbose=2", application]); record("verify-app");
        const staging = join(temporary, "image"); mkdirSync(staging);
        run("/usr/bin/ditto", [application, join(staging, "StackHarbor.app")]);
        symlinkSync("/Applications", join(staging, "Applications"));
        const dmgDirectory = join(bundle, "dmg"); mkdirSync(dmgDirectory, {recursive: true});
        const dmg = join(dmgDirectory, macOSInstallerName(config.productName, metadata.version, target));
        run("/usr/bin/hdiutil", ["create", "-volname", "StackHarbor", "-srcfolder", staging, "-ov", "-format", "UDZO", dmg]); record("create-dmg-from-stapled-app");
        run("/usr/bin/codesign", ["--force", "--sign", env.APPLE_SIGNING_IDENTITY, "--timestamp", dmg]); record("sign-dmg");
        await notarize(dmg, "dmg"); record("notarize-dmg");
        run("xcrun", ["stapler", "staple", dmg]); record("staple-dmg");
        run("/usr/bin/codesign", ["--verify", "--strict", dmg]);
        run("xcrun", ["stapler", "validate", dmg]);
        run("/usr/sbin/spctl", ["--assess", "--type", "open", "--context", "context:primary-signature", "--verbose=2", dmg]); record("verify-dmg");
        installers = [dmg];
      } finally { rmSync(temporary, {recursive: true, force: true}); }
    }
  }
  if (!installers.length) {
    for (const directory of readdirSync(bundle, {withFileTypes: true}).filter(entry => entry.isDirectory())) {
      for (const file of readdirSync(join(bundle, directory.name))) {
        if (file.startsWith(installerPrefix) && (file.endsWith(".dmg") || file.endsWith(".deb"))) installers.push(join(bundle, directory.name, file));
      }
    }
  }
  if (!installers.length) throw Error("Desktop bundling produced no installer");
  for (const installer of installers) cpSync(installer, join(output, installer.split("/").at(-1)));
  writeFileSync(join(output, "build.json"), JSON.stringify({...metadata, distribution, candidate, publication: candidate ? "local_only" : "not_published_by_build"}, null, 2) + "\n");
  writeFileSync(join(output, "README.txt"), release ? candidate ? "LOCAL SIGNED/NOTARIZED CANDIDATE. Not published or tagged. See build.json for dirty-source metadata.\n" : "Signed/notarized installers; validate platform signature before installation.\n" : "INTERNAL TEST BUILD. Not a signed/notarized public release. Installing starts no process.\n");
  writeFileSync(join(output, "SHA256SUMS"), readdirSync(output).filter(name => name !== "SHA256SUMS").sort().map(name => `${createHash("sha256").update(readFileSync(join(output, name))).digest("hex")}  ${name}`).join("\n") + "\n");
  console.log(`Desktop installers: ${output}`);
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await buildDesktop();
