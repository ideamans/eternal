export interface Rect {
  x: number
  y: number
  width: number
  height: number
}

export interface LayoutItem<T> {
  rect: Rect
  item: T
}

export interface Sizeable {
  cols: number
  rows: number
}

/**
 * Binary Space Partitioning layout.
 * Recursively splits the viewport into sub-regions, distributing items
 * proportionally based on their terminal area (cols × rows).
 * Split direction is chosen based on the current region's aspect ratio.
 */
export function bspLayout<T extends Sizeable>(items: T[], viewport: Rect): LayoutItem<T>[] {
  if (items.length === 0) return []
  if (items.length === 1) {
    return [{ rect: viewport, item: items[0] }]
  }

  // Split items into two groups at the midpoint
  const mid = Math.ceil(items.length / 2)
  const groupA = items.slice(0, mid)
  const groupB = items.slice(mid)

  // Weight each group by total terminal pixel area
  const areaA = groupA.reduce((sum, s) => sum + s.cols * s.rows, 0)
  const areaB = groupB.reduce((sum, s) => sum + s.cols * s.rows, 0)
  const ratio = areaA / (areaA + areaB)

  // Wider region → split vertically (left/right)
  // Taller region → split horizontally (top/bottom)
  if (viewport.width >= viewport.height) {
    const wA = Math.round(viewport.width * ratio)
    return [
      ...bspLayout(groupA, { x: viewport.x, y: viewport.y, width: wA, height: viewport.height }),
      ...bspLayout(groupB, { x: viewport.x + wA, y: viewport.y, width: viewport.width - wA, height: viewport.height }),
    ]
  } else {
    const hA = Math.round(viewport.height * ratio)
    return [
      ...bspLayout(groupA, { x: viewport.x, y: viewport.y, width: viewport.width, height: hA }),
      ...bspLayout(groupB, { x: viewport.x, y: viewport.y + hA, width: viewport.width, height: viewport.height - hA }),
    ]
  }
}
