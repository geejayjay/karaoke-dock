#!/usr/bin/env node
/**
 * Initializes and syncs the Capacitor Android project for `mobile` or `tv`,
 * applying LAN cleartext support, app icons, and Android TV Leanback manifest rules.
 */
import { execFileSync } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const target = process.argv[2]
if (target !== 'mobile' && target !== 'tv') {
  console.error('Usage: node clients/sync-android.mjs <mobile|tv>')
  process.exit(1)
}

const clientDir = resolve(root, 'clients', target)
const androidDir = resolve(clientDir, 'android')
const pkgPath = target === 'tv' ? 'app/karaokedock/tv' : 'app/karaokedock/mobile'
const npx = process.platform === 'win32' ? 'npx.cmd' : 'npx'

function cap(args) {
  execFileSync(npx, ['cap', ...args], { cwd: clientDir, stdio: 'inherit', shell: process.platform === 'win32' })
}

if (!existsSync(androidDir)) {
  cap(['add', 'android'])
}
cap(['sync', 'android'])

// 1. Copy custom MainActivity.java
const mainDst = resolve(androidDir, 'app/src/main/java', pkgPath, 'MainActivity.java')
mkdirSync(dirname(mainDst), { recursive: true })
copyFileSync(resolve(clientDir, 'overrides/MainActivity.java'), mainDst)

// 2. Copy KaraokeDock icons into Android mipmap folders
const icon512 = resolve(root, 'server/internal/api/icons/icon-512.png')
const icon192 = resolve(root, 'server/internal/api/icons/icon-192.png')
if (existsSync(icon512) && existsSync(icon192)) {
  for (const bucket of ['mipmap-mdpi', 'mipmap-hdpi', 'mipmap-xhdpi', 'mipmap-xxhdpi', 'mipmap-xxxhdpi']) {
    const dir = resolve(androidDir, 'app/src/main/res', bucket)
    mkdirSync(dir, { recursive: true })
    const src = bucket === 'mipmap-mdpi' || bucket === 'mipmap-hdpi' ? icon192 : icon512
    copyFileSync(src, resolve(dir, 'ic_launcher.png'))
    copyFileSync(src, resolve(dir, 'ic_launcher_round.png'))
    copyFileSync(src, resolve(dir, 'ic_launcher_foreground.png'))
  }
}

// 3. Patch AndroidManifest.xml for LAN cleartext HTTP, vibrate, and Android TV Leanback
const manifestPath = resolve(androidDir, 'app/src/main/AndroidManifest.xml')
let manifest = readFileSync(manifestPath, 'utf8')

if (!manifest.includes('usesCleartextTraffic')) {
  manifest = manifest.replace('<application', '<application\n        android:usesCleartextTraffic="true"')
}
if (target === 'mobile' && !manifest.includes('android.permission.VIBRATE')) {
  manifest = manifest.replace('</manifest>', '    <uses-permission android:name="android.permission.VIBRATE" />\n</manifest>')
}

if (target === 'tv') {
  const bannerDir = resolve(androidDir, 'app/src/main/res/drawable')
  mkdirSync(bannerDir, { recursive: true })
  copyFileSync(resolve(clientDir, 'overrides/tv_banner.xml'), resolve(bannerDir, 'tv_banner.xml'))

  if (!manifest.includes('android:banner=')) {
    manifest = manifest.replace('<application', '<application\n        android:banner="@drawable/tv_banner"')
  }
  if (!manifest.includes('android:screenOrientation="landscape"')) {
    manifest = manifest.replace('android:name=".MainActivity"', 'android:name=".MainActivity"\n            android:screenOrientation="landscape"')
  }
  if (!manifest.includes('LEANBACK_LAUNCHER')) {
    manifest = manifest.replace(
      '<category android:name="android.intent.category.LAUNCHER" />',
      '<category android:name="android.intent.category.LAUNCHER" />\n                <category android:name="android.intent.category.LEANBACK_LAUNCHER" />',
    )
  }
  if (!manifest.includes('android.software.leanback')) {
    const features = [
      '    <uses-feature android:name="android.software.leanback" android:required="false" />',
      '    <uses-feature android:name="android.hardware.touchscreen" android:required="false" />',
      '</manifest>',
    ].join('\n')
    manifest = manifest.replace('</manifest>', features)
  }
}

writeFileSync(manifestPath, manifest, 'utf8')

// 4. Sync versionName in app/build.gradle from client package.json (or VERSION env)
const gradlePath = resolve(androidDir, 'app/build.gradle')
if (existsSync(gradlePath)) {
  const pkg = JSON.parse(readFileSync(resolve(clientDir, 'package.json'), 'utf8'))
  const ver = (process.env.VERSION || pkg.version || '0.5.0-beta').replace(/^v/, '')
  let gradle = readFileSync(gradlePath, 'utf8')
  gradle = gradle.replace(/versionName\s+"[^"]*"/, `versionName "${ver}"`)
  writeFileSync(gradlePath, gradle, 'utf8')
}

console.log(`Synced Android project for ${target}.`)
