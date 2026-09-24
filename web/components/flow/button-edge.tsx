/*
React Flow UI's button-edge (https://ui.reactflow.dev/button-edge): an edge
with a control at its midpoint. The canvas puts its "+" there — insert a node
between these two — and the count of items the step before it produced.
*/
import { type ReactNode } from "react"
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from "@xyflow/react"

export function ButtonEdge({
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  style = {},
  markerEnd,
  children,
  className,
}: EdgeProps & { children: ReactNode; className?: string }) {
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })

  return (
    <>
      <BaseEdge path={edgePath} markerEnd={markerEnd} style={style} className={className} />
      <EdgeLabelRenderer>
        <div
          className="nodrag nopan pointer-events-auto absolute"
          style={{ transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)` }}
        >
          {children}
        </div>
      </EdgeLabelRenderer>
    </>
  )
}
