"use client"

/*
React Flow UI's zoom-slider (https://ui.reactflow.dev/zoom-slider), on Base UI:
its Slider reports a number (or an array) rather than always an array, and
icons inside a Button take no size classes — the Button sizes them.
*/
import { MaximizeIcon, MinusIcon, PlusIcon } from "lucide-react"
import { Panel, useReactFlow, useStore, useViewport, type PanelProps } from "@xyflow/react"

import { Button } from "@/components/ui/button"
import { Slider } from "@/components/ui/slider"
import { cn } from "@/lib/utils"

export function ZoomSlider({
  className,
  orientation = "horizontal",
  labels,
  ...props
}: Omit<PanelProps, "children"> & {
  orientation?: "horizontal" | "vertical"
  labels: { zoomIn: string; zoomOut: string; reset: string; fit: string; zoom: string }
}) {
  const { zoom } = useViewport()
  const { zoomTo, zoomIn, zoomOut, fitView } = useReactFlow()
  const minZoom = useStore((state) => state.minZoom)
  const maxZoom = useStore((state) => state.maxZoom)

  return (
    <Panel
      className={cn(
        "flex gap-1 rounded-md border border-nq-line bg-card p-1 text-foreground shadow-sm",
        orientation === "horizontal" ? "flex-row items-center" : "flex-col",
        className,
      )}
      {...props}
    >
      <div
        className={cn(
          "flex items-center gap-1",
          orientation === "horizontal" ? "flex-row" : "flex-col-reverse",
        )}
      >
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => zoomOut({ duration: 300 })}
          aria-label={labels.zoomOut}
        >
          <MinusIcon />
        </Button>
        <Slider
          className={cn("hidden sm:flex", orientation === "horizontal" ? "w-[120px]" : "h-[120px]")}
          orientation={orientation}
          value={[zoom]}
          min={minZoom}
          max={maxZoom}
          step={0.01}
          aria-label={labels.zoom}
          onValueChange={(v) => zoomTo(Array.isArray(v) ? v[0] : v)}
        />
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => zoomIn({ duration: 300 })}
          aria-label={labels.zoomIn}
        >
          <PlusIcon />
        </Button>
      </div>
      <Button
        className="min-w-14 tabular-nums"
        variant="ghost"
        size="sm"
        onClick={() => zoomTo(1, { duration: 300 })}
        aria-label={labels.reset}
      >
        {(100 * zoom).toFixed(0)}%
      </Button>
      <Button
        variant="ghost"
        size="icon-sm"
        onClick={() => fitView({ duration: 300, padding: 0.2 })}
        aria-label={labels.fit}
        data-fit-view
      >
        <MaximizeIcon />
      </Button>
    </Panel>
  )
}
