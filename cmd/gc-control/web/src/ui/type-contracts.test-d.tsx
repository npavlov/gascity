import {
  Button,
  Grid,
  IconButton,
  Input,
  Progress,
  SearchIcon,
  Spinner,
  Stack,
  StatusSignal,
} from "@/ui";

// @ts-expect-error style is intentionally excluded from shared controls.
<Button style={{ color: "red" }}>Run</Button>;
// @ts-expect-error color is intentionally excluded from shared controls.
<Input color="red" aria-label="Query" />;
// @ts-expect-error variants are closed semantic unions.
<Button variant="sparkly">Run</Button>;
// @ts-expect-error gaps must reference the shared spacing scale.
<Stack gap="7">Bad gap</Stack>;
// @ts-expect-error gaps must reference the shared spacing scale.
<Grid gap="12px">Bad gap</Grid>;
// @ts-expect-error IconButton always has a readable accessible name.
<IconButton icon={SearchIcon} />;
// @ts-expect-error status tones are a closed semantic union.
<StatusSignal label="Running" tone="celebration" />;
// @ts-expect-error progress always has a visible or accessible label.
<Progress value={50} />;
// @ts-expect-error standalone spinners always announce a readable label.
<Spinner />;
