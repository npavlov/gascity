import { forwardRef } from "react";

import type { UIIcon } from "../icons";
import { Button } from "./Button";
import type { ButtonProps } from "./Button";
import "./IconButton.css";

export interface IconButtonProps extends Omit<ButtonProps, "children"> {
  icon: UIIcon;
  "aria-label": string;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon: Icon, className, ...props },
  ref,
) {
  return (
    <Button ref={ref} className={["cc-icon-button", className].filter(Boolean).join(" ")} {...props}>
      <Icon aria-hidden="true" focusable="false" />
    </Button>
  );
});
