/**
 * Static export so the whole frontend compiles to plain HTML/JS/CSS under
 * ./dist, which the Go binary embeds via //go:embed and serves itself. No
 * Node server runs in production.
 *
 * @type {import('next').NextConfig}
 */
const nextConfig = {
  output: "export",
  distDir: "dist",
  images: { unoptimized: true },
  trailingSlash: true,
};

export default nextConfig;
