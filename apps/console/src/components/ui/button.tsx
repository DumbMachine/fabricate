import { Link } from "@tanstack/react-router"
import type { ReactNode } from "react"
import {
  buttonClassName,
  type ButtonSize,
  type ButtonVariant,
} from "@/components/motion/button/base"

export {
  Button,
  ButtonLink,
  buttonClassName,
} from "@/components/motion/button/base"
export type {
  ButtonLinkProps,
  ButtonProps,
  ButtonSize,
  ButtonVariant,
} from "@/components/motion/button/base"
export { StatefulButton } from "@/components/motion/button/stateful"
export type {
  ButtonState,
  StatefulButtonProps,
} from "@/components/motion/button/stateful"

type LinkButtonOwn = {
  variant?: ButtonVariant
  size?: ButtonSize
  className?: string
  children: ReactNode
}

type LinkButtonProps =
  | (LinkButtonOwn & { to: "/" })
  | (LinkButtonOwn & { to: "/environments" })
  | (LinkButtonOwn & {
      to: "/environments/$name"
      params: { name: string }
    })
  | (LinkButtonOwn & { to: "/sandboxes/$id"; params: { id: string } })
  | (LinkButtonOwn & { to: "/scenarios/$id"; params: { id: string } })

export function LinkButton(props: LinkButtonProps) {
  const className = buttonClassName(
    props.variant ?? "outline",
    props.size ?? "sm",
    props.className
  )
  switch (props.to) {
    case "/":
      return (
        <Link to="/" className={className}>
          {props.children}
        </Link>
      )
    case "/environments":
      return (
        <Link to="/environments" className={className}>
          {props.children}
        </Link>
      )
    case "/environments/$name":
      return (
        <Link
          to="/environments/$name"
          params={props.params}
          className={className}
        >
          {props.children}
        </Link>
      )
    case "/sandboxes/$id":
      return (
        <Link to="/sandboxes/$id" params={props.params} className={className}>
          {props.children}
        </Link>
      )
    case "/scenarios/$id":
      return (
        <Link to="/scenarios/$id" params={props.params} className={className}>
          {props.children}
        </Link>
      )
    default: {
      const neverProps: never = props
      return neverProps
    }
  }
}
