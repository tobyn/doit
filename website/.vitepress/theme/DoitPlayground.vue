<template>
  <div class="playground">
    <div class="playground-panels">
      <div class="playground-pane">
        <div class="playground-label">Source</div>
        <textarea
          v-model="source"
          class="playground-editor"
          spellcheck="false"
          @input="dirty = true"
        />
      </div>
      <div class="playground-pane">
        <div class="playground-label">Compiled JSON</div>
        <textarea
          :value="output"
          class="playground-editor"
          readonly
          spellcheck="false"
        />
      </div>
    </div>
    <div class="playground-toolbar">
      <button class="playground-btn" :disabled="loading" @click="compile">
        {{ loading ? 'Loading compiler...' : 'Compile' }}
      </button>
      <span v-if="error" class="playground-error">{{ error }}</span>
      <span v-if="warnings" class="playground-warnings">{{ warnings }}</span>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'

const defaultSource = `behavior Hello {
    let x = 1 + 2
    notify x
}`

// Precomputed output for the default source (avoids loading WASM on page load).
const defaultOutput = `{
    "0": {
        "op": "set_number",
        "1": 1,
        "2": 2,
        "cmt": "let x = 1 + 2"
    },
    "1": {
        "op": "notify",
        "0": "x"
    },
    "name": "Hello"
}`

const source = ref(defaultSource)
const output = ref(defaultOutput)
const error = ref('')
const warnings = ref('')
const loading = ref(false)
const dirty = ref(false)

let wasmReady = false
let wasmPromise = null

async function loadWasm() {
  if (wasmReady) return
  if (wasmPromise) return wasmPromise

  wasmPromise = (async () => {
    // Load wasm_exec.js
    await new Promise((resolve, reject) => {
      const script = document.createElement('script')
      script.src = import.meta.env.BASE_URL + 'wasm_exec.js'
      script.onload = resolve
      script.onerror = () => reject(new Error('Failed to load wasm_exec.js'))
      document.head.appendChild(script)
    })

    const go = new Go()
    const resp = await fetch(import.meta.env.BASE_URL + 'doit.wasm')
    const { instance } = await WebAssembly.instantiate(
      await resp.arrayBuffer(),
      go.importObject
    )
    go.run(instance)
    await new Promise(r => setTimeout(r, 50))
    wasmReady = true
  })()

  return wasmPromise
}

async function compile() {
  error.value = ''
  warnings.value = ''

  if (!dirty.value && source.value === defaultSource) {
    output.value = defaultOutput
    return
  }

  loading.value = true
  try {
    await loadWasm()
  } catch (e) {
    error.value = e.message
    loading.value = false
    return
  }
  loading.value = false

  const result = globalThis.doitCompile(source.value)
  if (result.error) {
    error.value = result.error
    output.value = ''
    return
  }
  if (result.warnings) {
    warnings.value = result.warnings.join('\n')
  }
  output.value = result.json || ''
}
</script>

<style scoped>
.playground {
  margin: 1.5rem 0;
  border: 1px solid var(--vp-c-divider);
  border-radius: 8px;
  overflow: hidden;
}

.playground-panels {
  display: flex;
  gap: 0;
}

@media (max-width: 640px) {
  .playground-panels {
    flex-direction: column;
  }
}

.playground-pane {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.playground-pane + .playground-pane {
  border-left: 1px solid var(--vp-c-divider);
}

@media (max-width: 640px) {
  .playground-pane + .playground-pane {
    border-left: none;
    border-top: 1px solid var(--vp-c-divider);
  }
}

.playground-label {
  padding: 6px 12px;
  font-size: 0.8em;
  font-weight: 600;
  color: var(--vp-c-text-2);
  background: var(--vp-c-bg-soft);
  border-bottom: 1px solid var(--vp-c-divider);
}

.playground-editor {
  font-family: var(--vp-font-family-mono);
  font-size: 13px;
  line-height: 1.6;
  padding: 12px;
  border: none;
  outline: none;
  resize: vertical;
  min-height: 180px;
  background: var(--vp-c-bg);
  color: var(--vp-c-text-1);
  tab-size: 4;
}

.playground-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: var(--vp-c-bg-soft);
  border-top: 1px solid var(--vp-c-divider);
}

.playground-btn {
  padding: 6px 16px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  border: none;
  border-radius: 4px;
  background: var(--vp-c-brand-1);
  color: var(--vp-c-white);
}

.playground-btn:hover:not(:disabled) {
  background: var(--vp-c-brand-2);
}

.playground-btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.playground-error {
  color: var(--vp-c-danger-1);
  font-family: var(--vp-font-family-mono);
  font-size: 0.8em;
  white-space: pre-wrap;
}

.playground-warnings {
  color: var(--vp-c-warning-1);
  font-family: var(--vp-font-family-mono);
  font-size: 0.8em;
  white-space: pre-wrap;
}
</style>
