/*
What makes the input look like a person rather than a program.

None of this is about defeating anybody's protections — the scope boundary on
FM-91 is explicit about that. It is about FIDELITY: a real pointer arrives
along a curve, slows as it lands, sometimes overshoots slightly and comes back;
a real hand types at an uneven rate and pauses at word boundaries. Pages are
built for that input, and they behave differently under a cursor that teleports
and a keystroke stream with no gaps — hover menus never open, drag thresholds
never trip, debounced search boxes fire once with the whole string.

So the motion here is the honest kind: it exists because it makes automation
WORK on real pages, and it is the same reason it looks real.
*/

/** Gaussian-ish sample, clamped. Sum of uniforms — cheap and good enough. */
export function jitter(mean, spread) {
  const u = (Math.random() + Math.random() + Math.random()) / 3 // ~normal
  return Math.max(1, mean + (u - 0.5) * 2 * spread)
}

export function randomBetween(min, max) {
  return min + Math.random() * (max - min)
}

/**
 * A curved path from one point to another.
 *
 * A cubic Bézier whose control points are pushed off the straight line by an
 * amount proportional to the distance, so short moves are nearly straight and
 * long ones sweep. Sampling is eased (slow, fast, slow), and the last few
 * points overshoot slightly and settle back — the correction a hand makes when
 * it lands.
 */
export function cursorPath(from, to, options = {}) {
  const distance = Math.hypot(to.x - from.x, to.y - from.y)
  const steps = Math.max(8, Math.min(60, Math.round(distance / 12) + 8))
  const bend = Math.min(distance * 0.28, 120)

  // Control points on alternating sides, so the arc is not always the same way.
  const side = Math.random() < 0.5 ? 1 : -1
  const normal = normalOf(from, to)
  const c1 = {
    x: from.x + (to.x - from.x) * 0.3 + normal.x * bend * side * randomBetween(0.4, 1),
    y: from.y + (to.y - from.y) * 0.3 + normal.y * bend * side * randomBetween(0.4, 1),
  }
  const c2 = {
    x: from.x + (to.x - from.x) * 0.7 + normal.x * bend * side * randomBetween(0.1, 0.6),
    y: from.y + (to.y - from.y) * 0.7 + normal.y * bend * side * randomBetween(0.1, 0.6),
  }

  const points = []
  for (let i = 1; i <= steps; i++) {
    const t = easeInOut(i / steps)
    points.push(bezier(from, c1, c2, to, t))
  }

  // Overshoot: only on moves long enough for a hand to have momentum.
  if (distance > 120 && options.overshoot !== false) {
    const angle = Math.atan2(to.y - from.y, to.x - from.x)
    const past = randomBetween(3, 11)
    points.push({ x: to.x + Math.cos(angle) * past, y: to.y + Math.sin(angle) * past })
    points.push({ x: to.x + Math.cos(angle) * past * 0.35, y: to.y + Math.sin(angle) * past * 0.35 })
    points.push({ x: to.x, y: to.y })
  }

  return points
}

/** Per-move delay: faster in the middle of a sweep, slower at the ends. */
export function moveDelay(index, total) {
  const middle = 1 - Math.abs(index / total - 0.5) * 2
  return jitter(11 - middle * 6, 3)
}

/**
 * Per-character delay.
 *
 * Longer after a space (word boundaries are where people pause) and after
 * punctuation, with the occasional longer gap for a person looking up at the
 * screen.
 */
export function keyDelay(character, previous) {
  let base = jitter(88, 38)
  if (previous === " ") base += jitter(40, 25)
  if (".,;:!?".includes(previous)) base += jitter(70, 40)
  if (Math.random() < 0.04) base += jitter(260, 160) // a glance away
  return base
}

/** Wheel notches rather than one jump: a page sees scroll as it really arrives. */
export function wheelSteps(total) {
  const steps = []
  let remaining = total
  const direction = Math.sign(total) || 1
  while (Math.abs(remaining) > 1) {
    const step = direction * Math.min(Math.abs(remaining), jitter(110, 45))
    steps.push(Math.round(step))
    remaining -= step
  }
  return steps
}

export function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, Math.max(0, ms)))
}

/** A point inside an element's box, but not dead centre — hands are not exact. */
export function pointWithin(rect) {
  const insetX = Math.min(rect.width * 0.28, 14)
  const insetY = Math.min(rect.height * 0.28, 10)
  return {
    x: rect.x + rect.width / 2 + randomBetween(-insetX, insetX),
    y: rect.y + rect.height / 2 + randomBetween(-insetY, insetY),
  }
}

function bezier(p0, p1, p2, p3, t) {
  const u = 1 - t
  return {
    x: u ** 3 * p0.x + 3 * u ** 2 * t * p1.x + 3 * u * t ** 2 * p2.x + t ** 3 * p3.x,
    y: u ** 3 * p0.y + 3 * u ** 2 * t * p1.y + 3 * u * t ** 2 * p2.y + t ** 3 * p3.y,
  }
}

function easeInOut(t) {
  return t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2
}

function normalOf(from, to) {
  const dx = to.x - from.x
  const dy = to.y - from.y
  const length = Math.hypot(dx, dy) || 1
  return { x: -dy / length, y: dx / length }
}
