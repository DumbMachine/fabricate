import {
  forwardRef,
  type ButtonHTMLAttributes,
  type AnchorHTMLAttributes,
} from "react"
import { cn } from "@/lib/utils"
export type ButtonVariant =
  "primary" | "secondary" | "ghost" | "outline" | "destructive"
export type ButtonSize = "sm" | "md" | "lg" | "icon"
export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant
  size?: ButtonSize
}
export type ButtonLinkProps = AnchorHTMLAttributes<HTMLAnchorElement> & {
  variant?: ButtonVariant
  size?: ButtonSize
}
const variants: Record<ButtonVariant, string> = {
  primary: "bg-primary text-primary-foreground hover:bg-primary/90",
  secondary: "bg-secondary text-secondary-foreground hover:bg-accent",
  ghost: "text-muted-foreground hover:bg-accent hover:text-foreground",
  outline: "border-border bg-transparent hover:bg-accent",
  destructive: "bg-destructive/10 text-destructive hover:bg-destructive/20",
}
const sizes: Record<ButtonSize, string> = {
  sm: "h-7 gap-1.5 px-2.5 text-[13px]",
  md: "h-8 gap-1.5 px-3 text-[13px]",
  lg: "h-9 gap-2 px-3.5 text-sm",
  icon: "size-8",
}
export const buttonClassName = (
  variant: ButtonVariant = "primary",
  size: ButtonSize = "md",
  extra?: string
) =>
  cn(
    "inline-flex shrink-0 items-center justify-center rounded-lg border border-transparent font-medium whitespace-nowrap transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-3.5 [&_svg]:shrink-0",
    variants[variant],
    sizes[size],
    extra
  )
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  function Button(
    { variant = "primary", size = "md", className, type = "button", ...props },
    ref
  ) {
    return (
      <button
        ref={ref}
        type={type}
        className={buttonClassName(variant, size, className)}
        {...props}
      />
    )
  }
)
export const ButtonLink = forwardRef<HTMLAnchorElement, ButtonLinkProps>(
  function ButtonLink(
    { variant = "primary", size = "md", className, ...props },
    ref
  ) {
    return (
      <a
        ref={ref}
        className={buttonClassName(variant, size, className)}
        {...props}
      />
    )
  }
)
