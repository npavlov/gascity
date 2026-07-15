import { createElement, forwardRef } from "react";
import type { LucideIcon, LucideProps } from "lucide-react";
import {
  Activity,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Container,
  Ellipsis,
  ExternalLink,
  FileDiff,
  Info,
  Landmark,
  ListChecks,
  Mail,
  MessageCircle,
  Pause,
  Play,
  RefreshCw,
  Search,
  Square,
  Terminal,
  TriangleAlert,
  Truck,
  X,
} from "lucide-react";

export type UIIcon = LucideIcon;

function withDefaults(Icon: LucideIcon): UIIcon {
  return forwardRef<SVGSVGElement, LucideProps>(function ControlCenterIcon(props, ref) {
    return createElement(Icon, { ref, size: 16, strokeWidth: 1.75, color: "currentColor", ...props });
  });
}

export const ActivityIcon = withDefaults(Activity);
export const AlertIcon = withDefaults(TriangleAlert);
export const CheckIcon = withDefaults(Check);
export const ChevronDownIcon = withDefaults(ChevronDown);
export const ChevronLeftIcon = withDefaults(ChevronLeft);
export const ChevronRightIcon = withDefaults(ChevronRight);
export const CloseIcon = withDefaults(X);
export const ContainerIcon = withDefaults(Container);
export const DiffIcon = withDefaults(FileDiff);
export const ExternalLinkIcon = withDefaults(ExternalLink);
export const InfoIcon = withDefaults(Info);
export const LandmarkIcon = withDefaults(Landmark);
export const ListChecksIcon = withDefaults(ListChecks);
export const MailIcon = withDefaults(Mail);
export const MessageIcon = withDefaults(MessageCircle);
export const MoreIcon = withDefaults(Ellipsis);
export const PauseIcon = withDefaults(Pause);
export const PlayIcon = withDefaults(Play);
export const RefreshIcon = withDefaults(RefreshCw);
export const SearchIcon = withDefaults(Search);
export const StopIcon = withDefaults(Square);
export const TerminalIcon = withDefaults(Terminal);
export const TruckIcon = withDefaults(Truck);
