<template>
  <div class="download-page">
    <h1>Download doit</h1>

    <div v-if="detected" class="download-primary">
      <p>Detected: <strong>{{ detected.label }}</strong></p>
      <a :href="detected.url" class="download-btn">
        Download {{ detected.filename }}
      </a>
    </div>

    <h2>All Downloads</h2>
    <p class="download-note">
      Downloads are attached to
      <a :href="releaseUrl">GitHub releases</a>.
    </p>

    <table class="download-table">
      <thead>
        <tr>
          <th>OS</th>
          <th>Architecture</th>
          <th>File</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="b in builds" :key="b.filename">
          <td>{{ b.os }}</td>
          <td>{{ b.arch }}</td>
          <td><a :href="b.url">{{ b.filename }}</a></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup>
import { computed, ref, onMounted } from 'vue'

const repo = 'tobyn/doit'
const releaseUrl = `https://github.com/${repo}/releases`

const builds = [
  { os: 'Windows', arch: 'x64',   goos: 'windows', goarch: 'amd64', ext: 'zip' },
  { os: 'Windows', arch: 'ARM64', goos: 'windows', goarch: 'arm64', ext: 'zip' },
  { os: 'macOS',   arch: 'x64',   goos: 'darwin',  goarch: 'amd64', ext: 'tar.gz' },
  { os: 'macOS',   arch: 'ARM64', goos: 'darwin',  goarch: 'arm64', ext: 'tar.gz' },
  { os: 'Linux',   arch: 'x64',   goos: 'linux',   goarch: 'amd64', ext: 'tar.gz' },
  { os: 'Linux',   arch: 'ARM64', goos: 'linux',   goarch: 'arm64', ext: 'tar.gz' },
].map(b => ({
  ...b,
  filename: `doit-${b.goos}-${b.goarch}.${b.ext}`,
  label: `${b.os} ${b.arch}`,
  // Placeholder URL; will point to the latest release asset once releases exist.
  url: `${releaseUrl}/latest/download/doit-${b.goos}-${b.goarch}.${b.ext}`,
}))

const platform = ref({ os: '', arch: '' })

onMounted(() => {
  const ua = navigator.userAgent.toLowerCase()
  const p = navigator.platform?.toLowerCase() || ''

  let os = ''
  if (ua.includes('win')) os = 'windows'
  else if (ua.includes('mac') || ua.includes('darwin')) os = 'darwin'
  else if (ua.includes('linux')) os = 'linux'

  let arch = 'amd64'
  // navigator.userAgentData is the modern way to detect ARM
  if (navigator.userAgentData?.architecture === 'arm') {
    arch = 'arm64'
  } else if (ua.includes('aarch64') || ua.includes('arm64') || p.includes('aarch64')) {
    arch = 'arm64'
  }

  platform.value = { os, arch }
})

const detected = computed(() => {
  if (!platform.value.os) return null
  return builds.find(
    b => b.goos === platform.value.os && b.goarch === platform.value.arch
  ) || null
})
</script>

<style scoped>
.download-page {
  max-width: 680px;
  margin: 0 auto;
  padding: 2rem 1rem;
}

.download-primary {
  margin: 1.5rem 0 2rem;
  padding: 1.5rem;
  border: 1px solid var(--vp-c-divider);
  border-radius: 8px;
  background: var(--vp-c-bg-soft);
  text-align: center;
}

.download-btn {
  display: inline-block;
  margin-top: 0.75rem;
  padding: 10px 24px;
  font-size: 15px;
  font-weight: 600;
  color: var(--vp-c-white);
  background: var(--vp-c-brand-1);
  border-radius: 6px;
  text-decoration: none;
}

.download-btn:hover {
  background: var(--vp-c-brand-2);
}

.download-note {
  margin-bottom: 1rem;
  color: var(--vp-c-text-2);
  font-size: 0.9em;
}

.download-table {
  width: 100%;
  border-collapse: collapse;
}

.download-table th,
.download-table td {
  padding: 8px 12px;
  text-align: left;
  border-bottom: 1px solid var(--vp-c-divider);
}

.download-table th {
  font-weight: 600;
  color: var(--vp-c-text-2);
  font-size: 0.85em;
}

.download-table a {
  color: var(--vp-c-brand-1);
}
</style>
