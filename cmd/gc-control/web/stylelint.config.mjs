const semanticProperties = [
  "/color$/",
  "/^background/",
  "/^border/",
  "/^outline/",
  "/^margin/",
  "/^padding/",
  "gap",
  "row-gap",
  "column-gap",
  "/^inset/",
  "top",
  "right",
  "bottom",
  "left",
  "border-radius",
  "box-shadow",
  "font-size",
  "line-height",
  "transition-duration",
  "animation-duration",
  "fill",
  "stroke",
];

const strictValues = [
  semanticProperties,
  {
    disableFix: true,
    ignoreValues: [
      "0",
      "auto",
      "none",
      "solid",
      "inherit",
      "currentColor",
      "transparent",
      "/^-?\\d+(?:\\.\\d+)?%$/",
      "/^-?\\d+(?:\\.\\d+)?fr$/",
    ],
  },
];

export default {
  extends: ["stylelint-config-standard"],
  plugins: ["stylelint-declaration-strict-value"],
  rules: {
    "custom-property-pattern": "^cc-",
    "declaration-block-no-duplicate-properties": true,
    "declaration-block-single-line-max-declarations": null,
    "property-no-unknown": true,
    "selector-class-pattern": "^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:__[a-z0-9]+(?:-[a-z0-9]+)*)?$",
  },
  overrides: [
    {
      files: ["src/**/*.css"],
      rules: {
        "color-no-hex": true,
        "color-named": "never",
        "function-disallowed-list": [
          "rgb",
          "rgba",
          "hsl",
          "hsla",
          "hwb",
          "lab",
          "lch",
          "oklab",
          "oklch",
          "color",
        ],
        "scale-unlimited/declaration-strict-value": strictValues,
      },
    },
    {
      files: ["**/src/ui/tokens.css", "**/src/ui/themes.css"],
      rules: {
        "color-no-hex": null,
        "color-named": null,
        "function-disallowed-list": null,
        "scale-unlimited/declaration-strict-value": null,
      },
    },
  ],
};
