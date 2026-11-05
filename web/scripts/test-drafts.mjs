// Exercise the real composable across isolated page-module instances sharing storage.
import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import ts from 'typescript'
import { createRenderer, nextTick, reactive } from 'vue'
const require = createRequire(import.meta.url)
const vueUrl = pathToFileURL(join(dirname(require.resolve('vue')), 'index.mjs')).href
const temp = await mkdtemp(join(tmpdir(), 'projnote-draft-test-'))
const saved = new Map(), session = new Map(), listeners = new Map()
globalThis.localStorage = { get length() { return saved.size }, key: i => [...saved.keys()][i] ?? null, getItem: k => saved.get(k) ?? null, setItem: (k, v) => saved.set(k, v), removeItem: k => saved.delete(k) }
globalThis.sessionStorage = { getItem: k => session.get(k) ?? null, setItem: (k, v) => session.set(k, v) }
globalThis.window = { addEventListener: (name, listener) => { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(listener) }, removeEventListener: (name, listener) => listeners.get(name)?.delete(listener), confirm: () => true }
const renderer = createRenderer({ patchProp() {}, insert() {}, remove() {}, createElement: () => ({}), createText: () => ({}), createComment: () => ({}), setText() {}, setElementText() {}, parentNode: () => null, nextSibling: () => null })
let appA, appB
try {
  const source = await readFile(new URL('../src/drafts.ts', import.meta.url), 'utf8')
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText.replace("from 'vue'", `from '${vueUrl}'`).replace("from './api'", "from './api.mjs'")
  await writeFile(join(temp, 'drafts.mjs'), compiled)
  await writeFile(join(temp, 'api.mjs'), `import {reactive} from '${vueUrl}';export const ui=reactive({datasetId:'dataset-a'});export class ApiError extends Error{};export const api=async()=>({});export const messageOf=e=>e.message;`)
  const runtime = pathToFileURL(join(temp, 'drafts.mjs')).href
  const { useDraft: useA } = await import(`${runtime}?tab=a`)
  const { useDraft: useB } = await import(`${runtime}?tab=b`)
  const formA = reactive({ title: 'original' }), formB = reactive({ title: 'original' })
  let a, b
  appA = renderer.createApp({ setup() { a = useA(() => 'projects:id', formA, () => true, () => 1); return () => null } })
  appB = renderer.createApp({ setup() { b = useB(() => 'projects:id', formB, () => true, () => 1); return () => null } })
  appA.mount({}); appB.mount({})
  await nextTick(); await nextTick()
  formA.title = 'tab A'; formB.title = 'tab B'; await nextTick(); a.persist(); b.persist()
  assert.equal(saved.size, 2, 'cloned sessionStorage must still produce separate writer keys')
  const bKey = [...saved.keys()].find(k => JSON.parse(saved.get(k)).value.title === 'tab B')
  a.discard(); assert(saved.has(bKey), 'saving A must preserve B'); assert.equal(saved.size, 1)
  await a.reset(); a.restore(); assert.equal(formA.title, 'tab B'); a.discard()
  assert(saved.has(bKey), 'saving restored content must preserve its other-writer source')
  await a.reset(); formB.title = 'tab B newer'; await nextTick(); b.persist(); a.discardAvailable()
  assert(saved.has(bKey), 'discarding a stale snapshot must preserve a newer copy')
  await a.reset(); a.discardAvailable(); assert(!saved.has(bKey), 'explicit matching-snapshot discard must remove the selected copy')
  const { ui } = await import(pathToFileURL(join(temp, 'api.mjs')).href)
  formB.title = 'dataset A draft'; await nextTick(); b.persist(); ui.datasetId = 'dataset-b'; await nextTick(); await nextTick()
  assert.equal(b.available.value, undefined, 'a different dataset must not expose drafts')
  console.log('PASS: writer isolation, save isolation, restored-source preservation, stale-snapshot protection, explicit discard, dataset isolation')
} finally {
  appA?.unmount(); appB?.unmount()
  await rm(temp, { recursive: true, force: true })
}
