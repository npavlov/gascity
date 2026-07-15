import { forwardRef } from "react";
import type { ButtonHTMLAttributes } from "react";

import "./Button.css";

export type ButtonVariant = "primary" | "secondary" | "danger" | "quiet";
export type ButtonSize = "compact" | "regular";
export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "style" | "color"> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { className, type = "button", variant = "secondary", size = "regular", ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={["cc-button", className].filter(Boolean).join(" ")}
      data-variant={variant}
      data-size={size}
      {...props}
    />
  );
});
