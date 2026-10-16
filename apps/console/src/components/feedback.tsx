import {
  Alert,
  AlertContent,
  AlertDescription,
  AlertIcon,
} from "@/components/motion/alert"
import { Loader, type LoaderVariant } from "@/components/motion/loader"
import { cn } from "cn"

export function LoadingLine({
  children,
  variant = "dots",
}: {
  children: string
  variant?: LoaderVariant
}) {
  return (
    <p className="flex items-center gap-2 text-sm text-muted-foreground">
      <span aria-hidden="true">
        <Loader variant={variant} size={16} label="" />
      </span>
      <span>{children}</span>
    </p>
  )
}

export function ErrorNote({
  children,
  compact = false,
}: {
  children: string
  compact?: boolean
}) {
  return (
    <Alert
      variant="destructive"
      className={cn(compact && "p-2")}
      contentClassName={cn(compact && "gap-2")}
    >
      <AlertIcon />
      <AlertContent>
        <AlertDescription className="text-destructive">
          {children}
        </AlertDescription>
      </AlertContent>
    </Alert>
  )
}
