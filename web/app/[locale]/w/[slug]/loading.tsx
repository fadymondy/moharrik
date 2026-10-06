import { LoadingRows } from "@/components/states"

// Shown inside the persistent workspace shell while the next page's code loads, so a click answers
// at once instead of freezing on the old page.
export default function Loading() {
  return (
    <div className="p-4 sm:p-6">
      <LoadingRows rows={4} />
    </div>
  )
}
