<script setup lang="ts">
import { onMounted, ref } from 'vue'
import BrandMark from './BrandMark.vue'
import { switchedManually } from './endpoint'
import { connectServer, discoverServers, readSavedHint, type DiscoveredServer } from './endpointHint'

const props = defineProps<{ mode: 'mobile' | 'tv' }>()
const emit = defineEmits<{ connected: [] }>()

const tab = ref<'auto' | 'manual'>('auto')
const scanning = ref(true)
const servers = ref<DiscoveredServer[]>([])
const picked = ref<DiscoveredServer | null>(null)

const address = ref(readSavedHint())
const room = ref('')
const showRoom = ref(false)
const busy = ref(false)
const error = ref('')

function hostOf(origin: string): string {
  return origin.replace(/^https?:\/\//, '')
}

async function scan() {
  error.value = ''
  picked.value = null
  scanning.value = true
  const found = await discoverServers()
  servers.value = found
  scanning.value = false

  // On Android TV first launch, auto-join when there is a single server on Wi-Fi.
  if (props.mode === 'tv' && found.length === 1 && !switchedManually.value) {
    await chooseServer(found[0])
    return
  }
  if (found.length === 0) {
    tab.value = 'manual'
  }
}

async function chooseServer(srv: DiscoveredServer) {
  error.value = ''
  if (props.mode === 'mobile' && srv.roomRequired && !room.value.trim()) {
    picked.value = srv
    showRoom.value = true
    return
  }
  busy.value = true
  const res = await connectServer(srv.origin, room.value)
  busy.value = false
  if (res.needRoom) {
    picked.value = srv
    showRoom.value = true
  }
  if (!res.ok) {
    error.value = res.error ?? "Couldn't connect."
    return
  }
  emit('connected')
}

async function submitManual() {
  if (busy.value) return
  const target = picked.value ? picked.value.origin : address.value
  error.value = ''
  busy.value = true
  const res = await connectServer(target, room.value)
  busy.value = false
  if (res.needRoom) showRoom.value = true
  if (!res.ok) {
    error.value = res.error ?? "Couldn't connect."
    return
  }
  emit('connected')
}

onMounted(() => {
  void scan()
})
</script>

<template>
  <main class="kd-connect" :class="{ tv: props.mode === 'tv' }">
    <div class="kd-connect-card">
      <div class="kd-connect-brand">
        <BrandMark />
      </div>
      <h1 class="kd-connect-title">{{ props.mode === 'tv' ? 'Connect this TV' : 'Join KaraokeDock' }}</h1>

      <div class="kd-tabs" role="tablist">
        <button
          type="button"
          class="kd-tab"
          :class="{ on: tab === 'auto' }"
          @click="tab = 'auto'; if (!scanning && !servers.length) scan()"
        >
          Auto-find
        </button>
        <button
          type="button"
          class="kd-tab"
          :class="{ on: tab === 'manual' }"
          @click="picked = null; tab = 'manual'"
        >
          Manual IP
        </button>
      </div>

      <!-- Discovered server needing a room code on mobile -->
      <form v-if="picked && tab === 'auto'" class="kd-connect-form" @submit.prevent="submitManual">
        <p class="kd-connect-sub">Joining <b>{{ picked.name }}</b> ({{ hostOf(picked.origin) }})</p>
        <input
          v-model="room"
          type="text"
          autocapitalize="characters"
          autocomplete="off"
          spellcheck="false"
          autofocus
          maxlength="32"
          placeholder="Room code on TV"
          class="kd-connect-input kd-connect-room"
        />
        <p v-if="error" class="kd-connect-err">{{ error }}</p>
        <button type="submit" class="kd-connect-btn" :disabled="busy">{{ busy ? 'Joining…' : 'Join room' }}</button>
        <button type="button" class="kd-connect-link" @click="picked = null; error = ''">Back to server list</button>
      </form>

      <!-- Auto-discovery tab -->
      <div v-else-if="tab === 'auto'" class="kd-connect-form">
        <div v-if="scanning" class="kd-scan-box">
          <span class="kd-pulse" />
          <span>Looking for KaraokeDock on Wi-Fi…</span>
        </div>

        <template v-else-if="servers.length">
          <p class="kd-connect-sub">Select your KaraokeDock server:</p>
          <button
            v-for="(s, idx) in servers"
            :key="s.origin"
            type="button"
            class="kd-server-item"
            :autofocus="idx === 0"
            :disabled="busy"
            @click="chooseServer(s)"
          >
            <span class="kd-server-name">{{ s.name }}</span>
            <span class="kd-server-addr">{{ hostOf(s.origin) }}</span>
          </button>
          <p v-if="error" class="kd-connect-err">{{ error }}</p>
          <button type="button" class="kd-connect-link" :disabled="busy" @click="scan">Scan again</button>
        </template>

        <div v-else class="kd-scan-empty">
          <p class="kd-connect-sub">No server answered on Wi-Fi yet.</p>
          <div class="kd-row">
            <button type="button" class="kd-secondary-btn" @click="scan">Scan again</button>
            <button type="button" class="kd-connect-btn" @click="tab = 'manual'">Enter IP manually</button>
          </div>
        </div>
      </div>

      <!-- Manual entry tab -->
      <form v-else class="kd-connect-form" @submit.prevent="submitManual">
        <p class="kd-connect-sub">
          {{
            props.mode === 'tv'
              ? 'Enter the IP address of your KaraokeDock server on Wi-Fi.'
              : 'Enter your server IP or paste the join link from the TV.'
          }}
        </p>
        <input
          v-model="address"
          type="url"
          inputmode="url"
          autocapitalize="none"
          autocomplete="off"
          spellcheck="false"
          autofocus
          :placeholder="props.mode === 'tv' ? '192.168.1.50:7464' : '192.168.1.50 or join link'"
          class="kd-connect-input"
        />

        <input
          v-if="props.mode === 'mobile' && showRoom"
          v-model="room"
          type="text"
          autocapitalize="characters"
          autocomplete="off"
          spellcheck="false"
          maxlength="32"
          placeholder="Room code on TV"
          class="kd-connect-input kd-connect-room"
        />

        <button
          v-else-if="props.mode === 'mobile'"
          type="button"
          class="kd-connect-link"
          @click="showRoom = true"
        >
          Have a room code?
        </button>

        <p v-if="error" class="kd-connect-err">{{ error }}</p>

        <button type="submit" class="kd-connect-btn" :disabled="busy">
          {{ busy ? 'Connecting…' : 'Connect' }}
        </button>
      </form>
    </div>
  </main>
</template>

<style scoped>
.kd-connect {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: grid;
  place-items: center;
  padding: 1.5rem;
  background: radial-gradient(circle at 50% 20%, rgba(143, 107, 255, 0.18), #0d0b14 68%);
  color: #fff;
  font-family: system-ui, -apple-system, sans-serif;
}
.kd-connect-card {
  width: 100%;
  max-width: 24rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: 0.75rem;
}
.kd-connect.tv .kd-connect-card {
  max-width: 30rem;
  gap: 1rem;
}
.kd-connect-brand {
  font-size: 2.5rem;
  margin-bottom: 0.15rem;
}
.kd-connect.tv .kd-connect-brand {
  font-size: 3.4rem;
}
.kd-connect-title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 800;
  letter-spacing: -0.02em;
}
.kd-connect.tv .kd-connect-title {
  font-size: 2rem;
}
.kd-tabs {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.35rem;
  width: 100%;
  padding: 0.25rem;
  border-radius: 0.85rem;
  background: #181426;
  border: 1px solid #2b2540;
}
.kd-tab {
  border: none;
  border-radius: 0.65rem;
  padding: 0.55rem 0.75rem;
  background: transparent;
  color: #b7b0cc;
  font-size: 0.9rem;
  font-weight: 700;
  cursor: pointer;
}
.kd-tab.on {
  background: #2b2540;
  color: #fff;
}
.kd-tab:focus-visible {
  outline: 2px solid #ff4d8d;
}
.kd-connect-sub {
  margin: 0;
  font-size: 0.92rem;
  color: #b7b0cc;
  line-height: 1.45;
}
.kd-connect.tv .kd-connect-sub {
  font-size: 1.08rem;
}
.kd-connect-form {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin-top: 0.25rem;
}
.kd-scan-box {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.65rem;
  padding: 1.4rem 1rem;
  border-radius: 0.9rem;
  background: #181426;
  border: 1px solid #2b2540;
  color: #d8d2ea;
  font-size: 0.95rem;
}
.kd-pulse {
  width: 0.65rem;
  height: 0.65rem;
  border-radius: 999px;
  background: #ff4d8d;
  box-shadow: 0 0 0 0 rgba(255, 77, 141, 0.7);
  animation: kd-ping 1.3s infinite;
}
@keyframes kd-ping {
  0% { box-shadow: 0 0 0 0 rgba(255, 77, 141, 0.7); }
  70% { box-shadow: 0 0 0 10px rgba(255, 77, 141, 0); }
  100% { box-shadow: 0 0 0 0 rgba(255, 77, 141, 0); }
}
.kd-server-item {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 1rem 1.1rem;
  border-radius: 0.9rem;
  border: 2px solid #2b2540;
  background: #181426;
  color: #fff;
  cursor: pointer;
  text-align: left;
}
.kd-server-item:hover,
.kd-server-item:focus-visible {
  border-color: #ff4d8d;
  background: #211b34;
  outline: none;
}
.kd-server-name {
  font-weight: 800;
  font-size: 1.05rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.kd-server-addr {
  font-size: 0.85rem;
  color: #b7b0cc;
  font-family: ui-monospace, monospace;
  flex-shrink: 0;
}
.kd-scan-empty {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
  padding: 0.75rem 0;
}
.kd-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.6rem;
}
.kd-secondary-btn {
  border: 1px solid #2b2540;
  border-radius: 0.9rem;
  background: #181426;
  color: #fff;
  padding: 0.95rem;
  font-size: 1rem;
  font-weight: 700;
  cursor: pointer;
}
.kd-secondary-btn:focus-visible {
  outline: 3px solid #ff4d8d;
}
.kd-connect-input {
  width: 100%;
  box-sizing: border-box;
  border-radius: 0.9rem;
  border: 2px solid #2b2540;
  background: #181426;
  color: #fff;
  padding: 0.95rem 1rem;
  font-size: 1.05rem;
  text-align: center;
  outline: none;
}
.kd-connect-input:focus {
  border-color: #ff4d8d;
  box-shadow: 0 0 0 3px rgba(255, 77, 141, 0.25);
}
.kd-connect-room {
  letter-spacing: 0.18em;
  font-weight: 700;
}
.kd-connect-link {
  align-self: center;
  background: none;
  border: none;
  color: #b7b0cc;
  font-size: 0.85rem;
  cursor: pointer;
  padding: 0.2rem 0.5rem;
}
.kd-connect-err {
  margin: 0;
  font-size: 0.9rem;
  color: #fca5a5;
}
.kd-connect-btn {
  width: 100%;
  border: none;
  border-radius: 0.9rem;
  background: #ff4d8d;
  color: #fff;
  padding: 1rem;
  font-size: 1.05rem;
  font-weight: 800;
  cursor: pointer;
}
.kd-connect-btn:focus-visible {
  outline: 3px solid #fff;
  outline-offset: 2px;
}
.kd-connect-btn:disabled {
  opacity: 0.6;
}
</style>
