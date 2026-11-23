// followViewport sizes the page to the browser's visible area for the life of
// the page. On Safari, and on Chrome for Android by default, the on-screen
// keyboard covers the page without resizing the layout viewport, and the
// browser pans the page to keep the focused field in view; only the visual
// viewport reports the area left visible. The layout reads the custom
// properties written here.
export function followViewport() {
  const viewport = window.visualViewport!;
  const style = document.documentElement.style;
  const follow = () => {
    // A pinch zoom magnifies the page instead of resizing it.
    if (viewport.scale > 1) return;
    style.setProperty("--viewport-top", `${viewport.offsetTop}px`);
    style.setProperty("--viewport-height", `${viewport.height}px`);
    style.setProperty("--keyboard-height", `${Math.max(0, window.innerHeight - viewport.height)}px`);
  };
  follow();
  viewport.addEventListener("resize", follow);
  viewport.addEventListener("scroll", follow);
}
