const fs = require("fs");
const { execSync } = require("child_process");

const commands = [];

function add(cmd) {
  commands.push(cmd);
}

// Palette constants adhering to Obsidian Precision
const C = {
  bg: "#0c0e12",
  bgPanel: "#121418",
  bgAlt: "#1a1c22",
  bgElevated: "#23262f",
  border: "#23262f",
  borderSubtle: "#1e2129",
  borderFocus: "#00F0FF",
  fg: "#ffffff",
  fgMuted: "#849495",
  fgDim: "#545b67",
  cyan: "#00F0FF",
  bull: "#00C087",
  bullDim: "#00C08726",
  bear: "#F6465D",
  bearDim: "#F6465D26",
  amber: "#FFB800",
  amberDim: "#FFB80026",
  white: "#FFFFFF",
};

// 1. Root Canvas Frame (1920 x 1080)
add({
  as: "canvas",
  command: "frame.create",
  params: {
    name: "Cerebro - Bybit Terminal & Trading Account",
    width: 1920,
    height: 1080,
    props: { fill: C.bg }
  }
});

// Helper for rectangles
function drawRect(asName, name, x, y, width, height, fill, stroke, radius = 0) {
  const props = { fill: fill || "none" };
  if (stroke) {
    props.stroke = stroke;
    props.strokeWeight = 1;
  }
  if (radius > 0) {
    props.cornerRadius = radius;
  }
  add({
    as: asName,
    command: "node.create",
    params: {
      parent: { "$ref": "canvas" },
      type: "rectangle",
      name: name,
      x: x,
      y: y,
      width: width,
      height: height,
      props: props
    }
  });
}

// Helper for text
function drawText(text, x, y, fontSize, fill, fontWeight = "400") {
  add({
    command: "text.create",
    params: {
      parent: { "$ref": "canvas" },
      text: String(text),
      x: x,
      y: y,
      props: {
        fontSize: fontSize,
        fontWeight: fontWeight,
        fill: fill
      }
    }
  });
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. TOP NAV / TICKER BAR (y: 0, height: 48)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("topNav", "Top Navigation Bar", 0, 0, 1920, 48, C.bgPanel, C.border);

// Brand & Environment
drawText("◈ CEREBRO", 16, 15, 15, C.cyan, "700");
drawRect("envBadge", "Paper Badge", 115, 14, 52, 20, C.amberDim, C.amber, 2);
drawText("PAPER", 124, 18, 10, C.amber, "700");

// Active Pair & Price Info
drawText("BTCUSDT Perpetual", 185, 15, 14, C.white, "600");
drawText("75,018.80", 330, 14, 16, C.bull, "700");
drawText("-1.08%", 415, 16, 12, C.bear, "600");

// Ticker Metadata Strip
const topStats = [
  { label: "Index Price", val: "75,022.50" },
  { label: "24h High", val: "76,420.00" },
  { label: "24h Low", val: "74,105.00" },
  { label: "24h Turnover(USDT)", val: "1,284,550,200" },
  { label: "Open Interest", val: "61,172.5 BTC" },
  { label: "Funding / Countdown", val: "-0.0052% / 04:18:28", color: C.amber }
];

let statX = 490;
topStats.forEach(s => {
  drawText(s.label, statX, 10, 10, C.fgDim);
  drawText(s.val, statX, 24, 11, s.color || C.white, "500");
  statX += 130;
});

// Top Nav Right Controls
drawText("● LIVE", 1680, 17, 11, C.bull, "600");
drawRect("btnFlatten", "Flatten Button", 1740, 12, 160, 24, C.bearDim, C.bear, 2);
drawText("EMERGENCY FLATTEN", 1756, 18, 10, C.bear, "700");

// ─────────────────────────────────────────────────────────────────────────────
// 2. LEFT SIDEBAR: WATCHLIST / MARKET PAIRS (x: 0, y: 48, width: 250, height: 992)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("leftSidebar", "Market Watch Sidebar", 0, 48, 250, 992, C.bgPanel, C.border);

// Search Bar
drawRect("searchBox", "Search Box", 10, 58, 230, 28, C.bgAlt, C.borderSubtle, 3);
drawText("🔍 Search symbol...", 20, 66, 11, C.fgDim);

// Category Tabs
const catTabs = ["Favorites", "Crypto", "TradFi", "Derivatives"];
catTabs.forEach((tab, i) => {
  const isDeriv = tab === "Derivatives";
  const tx = 12 + i * 58;
  drawText(tab, tx, 96, 11, isDeriv ? C.cyan : C.fgMuted, isDeriv ? "600" : "400");
  if (isDeriv) {
    drawRect(`catTabActive_${i}`, "Active Tab Line", tx, 110, 56, 2, C.cyan);
  }
});

// Watchlist Column Headers
drawRect("wlHeader", "Watchlist Header Row", 0, 114, 250, 26, C.bgAlt, C.borderSubtle);
drawText("Symbol", 12, 122, 10, C.fgDim, "600");
drawText("Last Price", 110, 122, 10, C.fgDim, "600");
drawText("24h Chg", 195, 122, 10, C.fgDim, "600");

// Watchlist Items
const watchlist = [
  { sym: "BTCUSDT", price: "75,018.80", chg: "-1.08%", isBull: false },
  { sym: "ETHUSDT", price: "2,143.32", chg: "-4.18%", isBull: false },
  { sym: "SOLUSDT", price: "188.45", chg: "+3.24%", isBull: true },
  { sym: "WLDUSDT", price: "0.3222", chg: "-2.57%", isBull: false },
  { sym: "MNTUSDT", price: "0.4484", chg: "-1.18%", isBull: false },
  { sym: "GMTUSDT", price: "0.00589", chg: "-2.73%", isBull: false },
  { sym: "OPUSDT", price: "0.0998", chg: "-1.49%", isBull: false },
  { sym: "DOGEUSDT", price: "0.18240", chg: "+5.12%", isBull: true },
  { sym: "AVAXUSDT", price: "27.85", chg: "-0.65%", isBull: false },
  { sym: "SUIUSDT", price: "2.15", chg: "+4.80%", isBull: true },
  { sym: "LINKUSDT", price: "12.45", chg: "+1.12%", isBull: true },
  { sym: "BNBUSDT", price: "594.10", chg: "+0.45%", isBull: true },
  { sym: "NEARUSDT", price: "4.82", chg: "-2.10%", isBull: false },
  { sym: "APTUSDT", price: "8.95", chg: "+1.85%", isBull: true }
];

watchlist.forEach((item, i) => {
  const y = 146 + i * 32;
  const isBtc = item.sym === "BTCUSDT";
  if (isBtc) {
    drawRect(`wlItemActive_${i}`, "Active Pair Highlight", 0, y - 4, 250, 32, C.bgElevated);
    drawRect(`wlItemActiveBar_${i}`, "Active Left Bar", 0, y - 4, 3, 32, C.cyan);
  }
  drawText(`★ ${item.sym}`, 12, y + 4, 11, isBtc ? C.cyan : C.white, "500");
  drawText(item.price, 110, y + 4, 11, C.white, "500");
  drawText(item.chg, 195, y + 4, 11, item.isBull ? C.bull : C.bear, "600");
});

// ─────────────────────────────────────────────────────────────────────────────
// 3. CENTER MAIN: CANDLESTICK CHART AREA (x: 250, y: 48, width: 990, height: 572)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("chartArea", "Chart Container", 250, 48, 990, 572, C.bg, C.border);

// Timeframe Toolbar (y: 48, height: 32)
drawRect("tfBar", "Timeframe Bar", 250, 48, 990, 32, C.bgPanel, C.border);
const timeframes = ["1s", "1m", "5m", "15m", "30m", "1h", "4h", "1D", "1W"];
timeframes.forEach((tf, i) => {
  const is15m = tf === "15m";
  drawText(tf, 265 + i * 40, 58, 11, is15m ? C.cyan : C.fgMuted, is15m ? "700" : "400");
  if (is15m) {
    drawRect(`tfLine_${i}`, "TF Indicator", 265 + i * 40, 76, 24, 2, C.cyan);
  }
});
drawText("Indicators: EMA 9 / EMA 21 · Volume SMA", 650, 58, 11, C.fgDim);
drawText("TradingView Standard", 1120, 58, 11, C.cyan);

// Chart Grid & Candles Canvas
drawRect("chartCanvas", "Chart Background", 250, 80, 990, 540, "#080a0d");

// Grid lines
for (let i = 1; i <= 5; i++) {
  const gy = 80 + i * 85;
  drawRect(`gridH_${i}`, "Grid Line H", 250, gy, 915, 1, "#15181f");
  drawText(`75,${500 - i * 100}.00`, 1175, gy - 6, 10, C.fgDim);
}

// Simulated Candlesticks (30 candles)
const candleData = [
  { x: 280, o: 300, c: 270, h: 260, l: 310, bull: true },
  { x: 305, o: 270, c: 290, h: 255, l: 300, bull: false },
  { x: 330, o: 290, c: 250, h: 240, l: 300, bull: true },
  { x: 355, o: 250, c: 230, h: 220, l: 260, bull: true },
  { x: 380, o: 230, c: 260, h: 225, l: 270, bull: false },
  { x: 405, o: 260, c: 240, h: 235, l: 265, bull: true },
  { x: 430, o: 240, c: 210, h: 200, l: 250, bull: true },
  { x: 455, o: 210, c: 225, h: 205, l: 235, bull: false },
  { x: 480, o: 225, c: 190, h: 180, l: 230, bull: true },
  { x: 505, o: 190, c: 215, h: 185, l: 220, bull: false },
  { x: 530, o: 215, c: 240, h: 210, l: 250, bull: false },
  { x: 555, o: 240, c: 220, h: 215, l: 245, bull: true },
  { x: 580, o: 220, c: 180, h: 175, l: 225, bull: true },
  { x: 605, o: 180, c: 170, h: 160, l: 190, bull: true },
  { x: 630, o: 170, c: 195, h: 165, l: 205, bull: false },
  { x: 655, o: 195, c: 230, h: 190, l: 240, bull: false },
  { x: 680, o: 230, c: 210, h: 200, l: 235, bull: true },
  { x: 705, o: 210, c: 170, h: 165, l: 215, bull: true },
  { x: 730, o: 170, c: 155, h: 145, l: 175, bull: true },
  { x: 755, o: 155, c: 180, h: 150, l: 190, bull: false },
  { x: 780, o: 180, c: 165, h: 160, l: 185, bull: true },
  { x: 805, o: 165, c: 140, h: 130, l: 170, bull: true },
  { x: 830, o: 140, c: 160, h: 135, l: 165, bull: false },
  { x: 855, o: 160, c: 135, h: 125, l: 165, bull: true },
  { x: 880, o: 135, c: 150, h: 130, l: 155, bull: false },
  { x: 905, o: 150, c: 130, h: 120, l: 155, bull: true },
  { x: 930, o: 130, c: 145, h: 125, l: 150, bull: false },
  { x: 955, o: 145, c: 120, h: 110, l: 150, bull: true },
  { x: 980, o: 120, c: 138, h: 115, l: 142, bull: false },
  { x: 1005, o: 138, c: 122, h: 112, l: 140, bull: true }
];

candleData.forEach((c, i) => {
  const color = c.bull ? C.bull : C.bear;
  const top = Math.min(c.o, c.c);
  const height = Math.max(4, Math.abs(c.o - c.c));
  // Wick
  drawRect(`wick_${i}`, "Candle Wick", c.x + 6, c.h, 2, c.l - c.h, color);
  // Body
  drawRect(`candle_${i}`, "Candle Body", c.x, top, 14, height, color);
  // Volume bar
  const volHeight = Math.floor(height * 1.5 + 5);
  drawRect(`vol_${i}`, "Vol Bar", c.x, 570 - volHeight, 14, volHeight, c.bull ? C.bullDim : C.bearDim);
});

// Current Price Line
drawRect("currPriceLine", "Price Level Line", 250, 202, 920, 1, C.bull);
drawRect("currPriceBadge", "Price Level Tag", 1170, 192, 70, 20, C.bull, null, 2);
drawText("75,018.80", 1175, 196, 11, "#000000", "700");

// ─────────────────────────────────────────────────────────────────────────────
// 4. ORDER BOOK & RECENT TRADES (x: 1240, y: 48, width: 340, height: 572)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("obPanel", "Order Book Container", 1240, 48, 340, 572, C.bgPanel, C.border);

// Header Tabs
drawText("Order Book", 1255, 60, 12, C.white, "600");
drawText("Recent Trades", 1345, 60, 12, C.fgDim);
drawRect("obPrecBtn", "Precision Selector", 1510, 54, 55, 20, C.bgAlt, C.borderSubtle, 2);
drawText("0.1 ▾", 1522, 58, 10, C.white);

// Table Columns
drawRect("obColHead", "OB Column Headers", 1240, 78, 340, 24, C.bgAlt, C.borderSubtle);
drawText("Price (USDT)", 1250, 84, 10, C.fgDim, "600");
drawText("Qty (BTC)", 1370, 84, 10, C.fgDim, "600");
drawText("Total (BTC)", 1490, 84, 10, C.fgDim, "600");

// Asks (Red)
const asks = [
  { p: "75,024.5", q: "1.420", t: "14.850", depth: 220 },
  { p: "75,023.0", q: "0.850", t: "13.430", depth: 195 },
  { p: "75,022.0", q: "2.150", t: "12.580", depth: 175 },
  { p: "75,021.0", q: "3.400", t: "10.430", depth: 140 },
  { p: "75,020.0", q: "1.820", t: "7.030", depth: 95 },
  { p: "75,019.5", q: "3.210", t: "5.210", depth: 70 },
  { p: "75,019.0", q: "2.000", t: "2.000", depth: 30 }
];

asks.forEach((a, i) => {
  const y = 106 + i * 26;
  drawRect(`askDepth_${i}`, "Ask Depth Bar", 1580 - a.depth, y, a.depth, 24, C.bearDim);
  drawText(a.p, 1250, y + 6, 11, C.bear, "600");
  drawText(a.q, 1370, y + 6, 11, C.white, "500");
  drawText(a.t, 1490, y + 6, 11, C.fgMuted, "400");
});

// Center Price / Spread Bar
drawRect("obSpreadBar", "OB Spread Bar", 1240, 292, 340, 36, C.bgAlt, C.borderSubtle);
drawText("↑ 75,018.80", 1250, 302, 15, C.bull, "700");
drawText("Index: 75,022.50", 1430, 304, 11, C.fgDim);

// Bids (Green)
const bids = [
  { p: "75,018.0", q: "4.120", t: "4.120", depth: 55 },
  { p: "75,017.5", q: "2.340", t: "6.460", depth: 85 },
  { p: "75,016.0", q: "3.900", t: "10.360", depth: 135 },
  { p: "75,015.0", q: "1.750", t: "12.110", depth: 160 },
  { p: "75,014.0", q: "5.100", t: "17.210", depth: 215 },
  { p: "75,012.5", q: "6.250", t: "23.460", depth: 275 },
  { p: "75,010.0", q: "4.800", t: "28.260", depth: 310 }
];

bids.forEach((b, i) => {
  const y = 332 + i * 26;
  drawRect(`bidDepth_${i}`, "Bid Depth Bar", 1580 - b.depth, y, b.depth, 24, C.bullDim);
  drawText(b.p, 1250, y + 6, 11, C.bull, "600");
  drawText(b.q, 1370, y + 6, 11, C.white, "500");
  drawText(b.t, 1490, y + 6, 11, C.fgMuted, "400");
});

// Depth Ratio Bar
drawRect("depthRatioBarBg", "Depth Ratio Bar", 1240, 520, 340, 24, C.bgAlt, C.borderSubtle);
drawRect("depthRatioGreen", "Bid Pressure", 1250, 526, 180, 12, C.bull, null, 2);
drawRect("depthRatioRed", "Ask Pressure", 1435, 526, 135, 12, C.bear, null, 2);
drawText("B 59%", 1250, 545, 10, C.bull, "600");
drawText("41% S", 1540, 545, 10, C.bear, "600");

// ─────────────────────────────────────────────────────────────────────────────
// 5. FAR RIGHT: TRADE EXECUTION, UNIFIED TRADING ACCOUNT, CONTRACT DETAILS
// (x: 1580, y: 48, width: 340, height: 992)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("rightPanel", "Right Control Panel", 1580, 48, 340, 992, C.bgPanel, C.border);

// Section 5A: Trade Execution Panel (y: 48, height: 350)
drawText("Trade", 1595, 60, 14, C.white, "700");
drawRect("tradeCrossBtn", "Margin Type Button", 1595, 82, 145, 28, C.bgAlt, C.borderSubtle, 3);
drawText("Cross ▾", 1640, 90, 11, C.white, "500");
drawRect("tradeLevBtn", "Leverage Selector", 1755, 82, 150, 28, C.bgAlt, C.borderSubtle, 3);
drawText("75.00x ▾", 1805, 90, 11, C.amber, "600");

// Order Types: Limit | Market | Chase Limit
drawText("Limit", 1600, 124, 11, C.fgMuted);
drawText("Market", 1660, 124, 11, C.white, "600");
drawRect("marketTabLine", "Market Tab Line", 1660, 138, 40, 2, C.cyan);
drawText("Chase Limit", 1730, 124, 11, C.fgMuted);

// Value Input Field
drawRect("tradeValInput", "Value Input", 1595, 148, 310, 36, C.bgAlt, C.borderSubtle, 3);
drawText("Value", 1608, 160, 11, C.fgDim);
drawText("500.00", 1795, 160, 12, C.white, "600");
drawText("USDT ▾", 1855, 160, 11, C.fgMuted);

// Percentage Slider Bar (0%, 25%, 50%, 75%, 100%)
drawRect("sliderTrack", "Slider Track", 1595, 202, 310, 4, C.bgElevated, null, 2);
drawRect("sliderFill", "Slider Filled Track", 1595, 202, 78, 4, C.cyan, null, 2);
// 5 Tick points
for (let i = 0; i < 5; i++) {
  const sx = 1595 + i * 77;
  drawRect(`sliderTick_${i}`, "Slider Point", sx - 4, 199, 10, 10, i === 1 ? C.cyan : C.border, null, 5);
}
drawText("0%", 1595, 214, 9, C.fgDim);
drawText("25%", 1668, 214, 9, C.cyan, "600");
drawText("50%", 1745, 214, 9, C.fgDim);
drawText("75%", 1822, 214, 9, C.fgDim);
drawText("100%", 1885, 214, 9, C.fgDim);

// Trade metrics
drawText("Quantity", 1595, 238, 11, C.fgDim);
drawText("0.050 / -- BTC", 1810, 238, 11, C.white);
drawText("Cost", 1595, 256, 11, C.fgDim);
drawText("500.00 USDT", 1825, 256, 11, C.white);
drawText("Liq. Price", 1595, 274, 11, C.fgDim);
drawText("73,850.00 USDT", 1805, 274, 11, C.amber);

// Checkbox options
drawText("☑ TP/SL", 1595, 298, 11, C.white);
drawText("☐ Reduce-Only", 1720, 298, 11, C.fgDim);

// Long & Short Dual Execution Buttons
drawRect("btnLong", "Long (Buy) Button", 1595, 322, 148, 38, C.bull, null, 3);
drawText("Long", 1650, 333, 14, "#000000", "700");
drawRect("btnShort", "Short (Sell) Button", 1757, 322, 148, 38, C.bear, null, 3);
drawText("Short", 1810, 333, 14, "#000000", "700");

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 5B: UNIFIED TRADING ACCOUNT PANEL (Item #1 Requested by User)
// (y: 378, height: 280)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("utaDividerTop", "UTA Top Divider", 1580, 376, 340, 1, C.border);

// Section Header with Eye & P&L icons
drawText("Unified Trading Account", 1595, 392, 13, C.white, "700");
drawText("👁", 1770, 392, 11, C.fgDim);
drawText("📊 P&L", 1860, 392, 11, C.cyan, "600");

// 1. Margin Mode
drawText("Margin Mode", 1595, 424, 11, C.fgMuted);
drawRect("marginModeBadge", "Margin Mode Pill", 1800, 418, 105, 22, C.bgAlt, C.borderSubtle, 2);
drawText("Cross Margin ›", 1812, 423, 11, C.white, "500");

// 2. Initial Margin (with meter gauge)
drawText("Initial Margin", 1595, 452, 11, C.fgMuted);
drawRect("imMeterBg", "IM Meter Background", 1700, 456, 120, 6, C.bgAlt, null, 3);
drawRect("imMeterFill", "IM Meter Fill", 1700, 456, 18, 6, C.cyan, null, 3);
drawText("0.00%", 1870, 452, 11, C.bull, "600");

// 3. Maintenance Margin (with meter gauge)
drawText("Maintenance Margin", 1595, 478, 11, C.fgMuted);
drawRect("mmMeterBg", "MM Meter Background", 1700, 482, 120, 6, C.bgAlt, null, 3);
drawRect("mmMeterFill", "MM Meter Fill", 1700, 482, 8, 6, C.cyan, null, 3);
drawText("0.00%", 1870, 478, 11, C.bull, "600");

// 4. Margin Balance
drawText("Margin Balance", 1595, 510, 11, C.fgMuted);
drawText("550.3596 USDT", 1800, 510, 13, C.white, "700");

// 5. Available Balance
drawText("Available Balance", 1595, 536, 11, C.fgMuted);
drawText("550.3596 USDT", 1800, 536, 13, C.cyan, "700");

// Quick Actions: Deposit | Convert | Transfer
const acBtns = ["Deposit", "Convert", "Transfer"];
acBtns.forEach((btn, i) => {
  const bx = 1595 + i * 105;
  drawRect(`utaBtn_${i}`, `UTA Action ${btn}`, bx, 566, 100, 28, C.bgAlt, C.borderSubtle, 2);
  drawText(btn, bx + 28, 574, 11, C.white, "500");
});

// Section 5C: Contract Details (y: 614, height: 350)
drawRect("utaDividerBot", "Contract Details Top Divider", 1580, 610, 340, 1, C.border);
drawText("Contract Details BTCUSDT", 1595, 626, 12, C.white, "700");

const contractSpecs = [
  { k: "Expiration Date", v: "Perpetual" },
  { k: "Index Price", v: "75,022.50 USDT" },
  { k: "Mark Price", v: "75,018.80 USDT" },
  { k: "Open Interest", v: "61,172.50 BTC" },
  { k: "24H Turnover", v: "1,284,550,200 USDT" },
  { k: "24H Volume", v: "18,452.80 BTC" },
  { k: "Max Leverage", v: "100x" },
  { k: "Settlement Asset", v: "USDT" }
];

contractSpecs.forEach((spec, i) => {
  const sy = 654 + i * 26;
  drawText(spec.k, 1595, sy, 11, C.fgDim);
  drawText(spec.v, 1780, sy, 11, C.white, "500");
});

// ─────────────────────────────────────────────────────────────────────────────
// 6. BOTTOM DOCKED PANEL: POSITIONS / ORDERS / LOGS
// (x: 250, y: 620, width: 1330, height: 420)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("dockedPanel", "Bottom Docked Panel", 250, 620, 1330, 420, C.bgPanel, C.border);

// Bottom Tabs Bar
drawRect("dockTabsBar", "Dock Tabs Bar", 250, 620, 1330, 34, C.bgAlt, C.borderSubtle);
const dockTabs = [
  { name: "Positions (1)", active: true },
  { name: "Open Orders (0)" },
  { name: "Order History" },
  { name: "Trade History" },
  { name: "Assets" },
  { name: "Borrowings (0)" },
  { name: "P&L Analysis" },
  { name: "Agent Telemetry & Logs" }
];

dockTabs.forEach((dt, i) => {
  const dx = 265 + i * 140;
  drawText(dt.name, dx, 631, 11, dt.active ? C.white : C.fgMuted, dt.active ? "700" : "400");
  if (dt.active) {
    drawRect(`dockTabLine_${i}`, "Active Dock Tab Line", dx, 652, 85, 2, C.cyan);
  }
});

// Positions Table Header
drawRect("posTableHead", "Positions Table Header", 250, 654, 1330, 28, C.bgElevated, C.borderSubtle);
const posCols = [
  { label: "Contracts", x: 265 },
  { label: "Qty / Value", x: 440 },
  { label: "Entry Price", x: 580 },
  { label: "Mark Price", x: 700 },
  { label: "Liq. Price", x: 820 },
  { label: "IM / MM (Margin)", x: 940 },
  { label: "Unrealized P&L (ROI)", x: 1100 },
  { label: "TP / SL", x: 1280 },
  { label: "Action", x: 1460 }
];

posCols.forEach(col => {
  drawText(col.label, col.x, 662, 10, C.fgDim, "600");
});

// Position Row 1 (Active BTCUSDT Long)
drawRect("posRow1", "Position Row 1", 250, 682, 1330, 44, C.bgPanel, C.borderSubtle);
drawRect("posRow1SideBar", "Position Long Side Bar", 250, 682, 3, 44, C.bull);

// Symbol + Side + Leverage
drawText("BTCUSDT Perpetual", 265, 692, 12, C.white, "700");
drawRect("posLongBadge", "Long Badge", 265, 710, 36, 14, C.bullDim, C.bull, 2);
drawText("LONG", 270, 712, 9, C.bull, "700");
drawRect("posLevBadge", "Leverage Badge", 306, 710, 26, 14, C.bgElevated, null, 2);
drawText("10x", 310, 712, 9, C.fgMuted, "600");

// Qty & Value
drawText("0.150 BTC", 440, 692, 12, C.white, "500");
drawText("11,252.82 USDT", 440, 710, 10, C.fgDim);

// Entry, Mark, Liq Price
drawText("74,800.00", 580, 694, 12, C.white, "500");
drawText("75,018.80", 700, 694, 12, C.white, "500");
drawText("68,140.00", 820, 694, 12, C.amber, "600");

// IM / MM
drawText("1,125.28 USDT", 940, 692, 12, C.white, "500");
drawText("Cross (MM: 56.26 USDT)", 940, 710, 10, C.fgDim);

// Unrealized PnL & ROI
drawText("+32.82 USDT", 1100, 692, 13, C.bull, "700");
drawText("+2.92% ROI", 1100, 710, 10, C.bull, "600");

// TP / SL
drawText("TP: 77,200.00", 1280, 692, 11, C.bull);
drawText("SL: 73,500.00", 1280, 710, 11, C.bear);

// Close Actions
drawRect("btnCloseMkt", "Market Close Button", 1460, 692, 54, 24, C.bearDim, C.bear, 2);
drawText("Market", 1470, 698, 10, C.bear, "600");
drawRect("btnCloseLmt", "Limit Close Button", 1520, 692, 50, 24, C.bgAlt, C.borderSubtle, 2);
drawText("Limit", 1532, 698, 10, C.fgMuted, "500");

// ─────────────────────────────────────────────────────────────────────────────
// 7. BOTTOM RUNNING TICKER TAPE (y: 1040, height: 40)
// ─────────────────────────────────────────────────────────────────────────────
drawRect("bottomTicker", "Bottom Running Ticker Tape", 0, 1040, 1920, 40, C.bgPanel, C.border);

const tickerItems = [
  { s: "BTCUSDT", c: "-1.08%", b: false },
  { s: "ETHUSDT", c: "-4.18%", b: false },
  { s: "SOLUSDT", c: "+3.24%", b: true },
  { s: "WLDUSDT", c: "-2.57%", b: false },
  { s: "MNTUSDT", c: "-1.18%", b: false },
  { s: "GMTUSDT", c: "-2.73%", b: false },
  { s: "OPUSDT", c: "-1.49%", b: false },
  { s: "DOGEUSDT", c: "+5.12%", b: true },
  { s: "AVAXUSDT", c: "-0.65%", b: false },
  { s: "SUIUSDT", c: "+4.80%", b: true },
  { s: "NEARUSDT", c: "-2.10%", b: false },
  { s: "BNBUSDT", c: "+0.45%", b: true }
];

let tx = 20;
tickerItems.forEach((t, i) => {
  drawText(`★ ${t.s}`, tx, 1054, 11, C.fgMuted, "500");
  drawText(t.c, tx + 80, 1054, 11, t.b ? C.bull : C.bear, "600");
  drawText("·", tx + 130, 1054, 11, C.fgDim);
  tx += 148;
});

// Write to JSON script and execute with figcraft-cli
const outScript = "/tmp/figcraft_bybit_run.json";
fs.writeFileSync(outScript, JSON.stringify(commands, null, 2));
console.log(`Generated FigCraft script with ${commands.length} commands.`);
