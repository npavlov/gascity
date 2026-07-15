import type { ReactElement, ReactNode } from "react";
import * as RadixTooltip from "@radix-ui/react-tooltip";

import "./Tooltip.css";

export interface TooltipProps { children: ReactElement; content: ReactNode }

export function Tooltip({ children, content }: TooltipProps) {
  return <RadixTooltip.Provider delayDuration={0}><RadixTooltip.Root><RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger><RadixTooltip.Portal><RadixTooltip.Content className="cc-tooltip" sideOffset={4}>{content}</RadixTooltip.Content></RadixTooltip.Portal></RadixTooltip.Root></RadixTooltip.Provider>;
}
