const GRADIENTS = [
  ['#7c3aed', '#db2777'],
  ['#0ea5e9', '#6366f1'],
  ['#f97316', '#e11d48'],
  ['#10b981', '#0e7490'],
  ['#eab308', '#ea580c'],
  ['#ec4899', '#8b5cf6'],
  ['#14b8a6', '#4f46e5'],
  ['#ef4444', '#9333ea'],
]

/** The same text always gets the same colors. */
export function gradientFor(text: string): string {
  let hash = 0
  for (let i = 0; i < text.length; i++) hash = (hash * 31 + text.charCodeAt(i)) | 0
  const [a, b] = GRADIENTS[Math.abs(hash) % GRADIENTS.length]
  return `linear-gradient(135deg, ${a}, ${b})`
}
