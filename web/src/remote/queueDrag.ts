import { reactive } from 'vue'

/** The song being carried in the queue, shared so the other rows can make room. */
export const queueDrag = reactive({ id: 0, from: -1, to: -1 })

/** How far a row at `index` moves aside while another row is carried, in rows. */
export function shiftFor(index: number, id: number): number {
  const { from, to } = queueDrag
  if (!queueDrag.id || id === queueDrag.id) return 0
  if (from < index && index <= to) return -1
  if (to <= index && index < from) return 1
  return 0
}
