import { Outlet, createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/environments")({
  component: EnvironmentsLayout,
})

function EnvironmentsLayout() {
  return <Outlet />
}
