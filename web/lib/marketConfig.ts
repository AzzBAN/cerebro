import YAML from "yaml";

export interface MarketSymbolConfig {
  symbol: string;
  leverage?: number;
  strategies?: string[];
  tick_size?: number;
  lot_size?: number;
  timeframes?: string[];
  primary_timeframe?: string;
  trend_timeframe?: string;
  max_spread_pct?: number;
}

export interface VenueDefaultsConfig {
  contract_type?: string;
  leverage?: number;
  margin_type?: string;
  tick_size?: number;
  lot_size?: number;
  min_notional?: number;
  max_order_notional?: number;
  max_position_size_pct?: number;
  timeframes?: string[];
  primary_timeframe?: string;
  trend_timeframe?: string;
  max_spread_pct?: number;
  enabled?: boolean;
}

export interface VenueData {
  venue: string;
  defaults: VenueDefaultsConfig;
  symbols: MarketSymbolConfig[];
}

export interface CryptoPreset {
  symbol: string;
  name: string;
  base: string;
  category: "Major" | "L1/L2" | "DeFi/AI" | "Meme" | "Commodity";
  defaultLeverage: number;
}

export const POPULAR_FUTURES_MARKETS: CryptoPreset[] = [
  // Major
  { symbol: "BTC/USDT-PERP", name: "Bitcoin", base: "BTC", category: "Major", defaultLeverage: 50 },
  { symbol: "ETH/USDT-PERP", name: "Ethereum", base: "ETH", category: "Major", defaultLeverage: 50 },
  { symbol: "SOL/USDT-PERP", name: "Solana", base: "SOL", category: "Major", defaultLeverage: 50 },
  { symbol: "BNB/USDT-PERP", name: "BNB", base: "BNB", category: "Major", defaultLeverage: 50 },
  // L1 / L2
  { symbol: "DOGE/USDT-PERP", name: "Dogecoin", base: "DOGE", category: "Meme", defaultLeverage: 50 },
  { symbol: "XRP/USDT-PERP", name: "Ripple", base: "XRP", category: "Major", defaultLeverage: 50 },
  { symbol: "ADA/USDT-PERP", name: "Cardano", base: "ADA", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "AVAX/USDT-PERP", name: "Avalanche", base: "AVAX", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "SUI/USDT-PERP", name: "Sui", base: "SUI", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "NEAR/USDT-PERP", name: "NEAR Protocol", base: "NEAR", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "APT/USDT-PERP", name: "Aptos", base: "APT", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "DOT/USDT-PERP", name: "Polkadot", base: "DOT", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "LINK/USDT-PERP", name: "Chainlink", base: "LINK", category: "DeFi/AI", defaultLeverage: 50 },
  { symbol: "MATIC/USDT-PERP", name: "Polygon", base: "MATIC", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "LTC/USDT-PERP", name: "Litecoin", base: "LTC", category: "Major", defaultLeverage: 50 },
  { symbol: "ARB/USDT-PERP", name: "Arbitrum", base: "ARB", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "OP/USDT-PERP", name: "Optimism", base: "OP", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "TIA/USDT-PERP", name: "Celestia", base: "TIA", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "SEI/USDT-PERP", name: "Sei", base: "SEI", category: "L1/L2", defaultLeverage: 50 },
  { symbol: "INJ/USDT-PERP", name: "Injective", base: "INJ", category: "DeFi/AI", defaultLeverage: 50 },
  // Meme
  { symbol: "PEPE/USDT-PERP", name: "Pepe", base: "PEPE", category: "Meme", defaultLeverage: 50 },
  { symbol: "SHIB/USDT-PERP", name: "Shiba Inu", base: "SHIB", category: "Meme", defaultLeverage: 50 },
  { symbol: "WIF/USDT-PERP", name: "dogwifhat", base: "WIF", category: "Meme", defaultLeverage: 50 },
  // DeFi & AI
  { symbol: "FET/USDT-PERP", name: "Artificial Superintelligence", base: "FET", category: "DeFi/AI", defaultLeverage: 50 },
  { symbol: "RENDER/USDT-PERP", name: "Render", base: "RENDER", category: "DeFi/AI", defaultLeverage: 50 },
  { symbol: "TAO/USDT-PERP", name: "Bittensor", base: "TAO", category: "DeFi/AI", defaultLeverage: 50 },
  { symbol: "AAVE/USDT-PERP", name: "Aave", base: "AAVE", category: "DeFi/AI", defaultLeverage: 50 },
  { symbol: "UNI/USDT-PERP", name: "Uniswap", base: "UNI", category: "DeFi/AI", defaultLeverage: 50 },
  { symbol: "PENDLE/USDT-PERP", name: "Pendle", base: "PENDLE", category: "DeFi/AI", defaultLeverage: 50 },
  // Commodity
  { symbol: "XAU/USDT-PERP", name: "Gold Perpetual", base: "XAU", category: "Commodity", defaultLeverage: 20 },
];

export const POPULAR_SPOT_MARKETS: CryptoPreset[] = [
  { symbol: "BTC/USDT", name: "Bitcoin", base: "BTC", category: "Major", defaultLeverage: 1 },
  { symbol: "ETH/USDT", name: "Ethereum", base: "ETH", category: "Major", defaultLeverage: 1 },
  { symbol: "SOL/USDT", name: "Solana", base: "SOL", category: "Major", defaultLeverage: 1 },
  { symbol: "BNB/USDT", name: "BNB", base: "BNB", category: "Major", defaultLeverage: 1 },
  { symbol: "DOGE/USDT", name: "Dogecoin", base: "DOGE", category: "Meme", defaultLeverage: 1 },
  { symbol: "XRP/USDT", name: "Ripple", base: "XRP", category: "Major", defaultLeverage: 1 },
  { symbol: "ADA/USDT", name: "Cardano", base: "ADA", category: "L1/L2", defaultLeverage: 1 },
  { symbol: "AVAX/USDT", name: "Avalanche", base: "AVAX", category: "L1/L2", defaultLeverage: 1 },
  { symbol: "LINK/USDT", name: "Chainlink", base: "LINK", category: "DeFi/AI", defaultLeverage: 1 },
  { symbol: "NEAR/USDT", name: "NEAR Protocol", base: "NEAR", category: "L1/L2", defaultLeverage: 1 },
  { symbol: "SUI/USDT", name: "Sui", base: "SUI", category: "L1/L2", defaultLeverage: 1 },
  { symbol: "DOT/USDT", name: "Polkadot", base: "DOT", category: "L1/L2", defaultLeverage: 1 },
  { symbol: "PEPE/USDT", name: "Pepe", base: "PEPE", category: "Meme", defaultLeverage: 1 },
  { symbol: "SHIB/USDT", name: "Shiba Inu", base: "SHIB", category: "Meme", defaultLeverage: 1 },
  { symbol: "LTC/USDT", name: "Litecoin", base: "LTC", category: "Major", defaultLeverage: 1 },
  { symbol: "UNI/USDT", name: "Uniswap", base: "UNI", category: "DeFi/AI", defaultLeverage: 1 },
];

export const AVAILABLE_STRATEGIES = [
  { id: "scalping", label: "Scalping", desc: "1m/5m fast momentum, tight SL" },
  { id: "mean_reversion", label: "Mean Reversion", desc: "RSI & Bollinger band bounds" },
  { id: "trend_following", label: "Trend Following", desc: "Multi-timeframe EMA crossover" },
  { id: "volatility_breakout", label: "Volatility Breakout", desc: "ATR range breakouts" },
  { id: "swing", label: "Swing Trading", desc: "4h/1d macro trend rides" },
];

export function parseMarketsYaml(yamlText: string): { venues: VenueData[]; doc: YAML.Document } {
  const doc = YAML.parseDocument(yamlText);
  const venuesNode = doc.get("venues");
  const venues: VenueData[] = [];

  if (venuesNode && (venuesNode as YAML.YAMLSeq).items) {
    const items = (venuesNode as YAML.YAMLSeq).items as YAML.YAMLMap[];
    for (const item of items) {
      if (!item || !item.get) continue;
      const venueName = String(item.get("venue") || "");
      const defaultsObj = (item.get("defaults") ? (item.get("defaults") as YAML.YAMLMap).toJSON() : {}) as VenueDefaultsConfig;
      
      const symbolsNode = item.get("symbols") as YAML.YAMLSeq | undefined;
      const symbolsList: MarketSymbolConfig[] = [];
      if (symbolsNode && symbolsNode.items) {
        for (const symItem of symbolsNode.items as YAML.YAMLMap[]) {
          if (symItem && symItem.toJSON) {
            symbolsList.push(symItem.toJSON() as MarketSymbolConfig);
          }
        }
      }

      venues.push({
        venue: venueName,
        defaults: defaultsObj,
        symbols: symbolsList,
      });
    }
  }

  return { venues, doc };
}

export function addSymbolToDoc(
  doc: YAML.Document,
  venueName: string,
  symbolConfig: MarketSymbolConfig
): string {
  const venuesNode = doc.get("venues") as YAML.YAMLSeq;
  if (!venuesNode || !venuesNode.items) {
    throw new Error("Invalid markets.yaml: no venues sequence found");
  }

  const targetVenue = venuesNode.items.find((v) => {
    const map = v as YAML.YAMLMap;
    return map && map.get && map.get("venue") === venueName;
  }) as YAML.YAMLMap | undefined;

  if (!targetVenue) {
    throw new Error(`Venue ${venueName} not found in markets.yaml`);
  }

  let symbolsNode = targetVenue.get("symbols") as YAML.YAMLSeq | undefined;
  if (!symbolsNode) {
    const newSeq = doc.createNode([]);
    targetVenue.set("symbols", newSeq);
    symbolsNode = targetVenue.get("symbols") as YAML.YAMLSeq;
  }

  // Check if symbol already exists
  const existing = symbolsNode.items.some((item) => {
    const map = item as YAML.YAMLMap;
    return map && map.get && map.get("symbol") === symbolConfig.symbol;
  });

  if (existing) {
    throw new Error(`Symbol ${symbolConfig.symbol} already exists in ${venueName}`);
  }

  // Clean object for YAML node creation
  const nodeData: Record<string, unknown> = {
    symbol: symbolConfig.symbol,
  };
  if (symbolConfig.leverage !== undefined && venueName !== "binance_spot") {
    nodeData.leverage = symbolConfig.leverage;
  }
  if (symbolConfig.strategies && symbolConfig.strategies.length > 0) {
    nodeData.strategies = symbolConfig.strategies;
  }

  symbolsNode.add(doc.createNode(nodeData));
  return doc.toString();
}

export function removeSymbolFromDoc(
  doc: YAML.Document,
  venueName: string,
  symbol: string
): string {
  const venuesNode = doc.get("venues") as YAML.YAMLSeq;
  if (!venuesNode || !venuesNode.items) return doc.toString();

  const targetVenue = venuesNode.items.find((v) => {
    const map = v as YAML.YAMLMap;
    return map && map.get && map.get("venue") === venueName;
  }) as YAML.YAMLMap | undefined;

  if (!targetVenue) return doc.toString();

  const symbolsNode = targetVenue.get("symbols") as YAML.YAMLSeq | undefined;
  if (!symbolsNode || !symbolsNode.items) return doc.toString();

  const idx = symbolsNode.items.findIndex((item) => {
    const map = item as YAML.YAMLMap;
    return map && map.get && map.get("symbol") === symbol;
  });

  if (idx !== -1) {
    symbolsNode.delete(idx);
  }

  return doc.toString();
}

export function updateSymbolInDoc(
  doc: YAML.Document,
  venueName: string,
  symbol: string,
  updates: Partial<MarketSymbolConfig>
): string {
  const venuesNode = doc.get("venues") as YAML.YAMLSeq;
  if (!venuesNode || !venuesNode.items) return doc.toString();

  const targetVenue = venuesNode.items.find((v) => {
    const map = v as YAML.YAMLMap;
    return map && map.get && map.get("venue") === venueName;
  }) as YAML.YAMLMap | undefined;

  if (!targetVenue) return doc.toString();

  const symbolsNode = targetVenue.get("symbols") as YAML.YAMLSeq | undefined;
  if (!symbolsNode || !symbolsNode.items) return doc.toString();

  const symItem = symbolsNode.items.find((item) => {
    const map = item as YAML.YAMLMap;
    return map && map.get && map.get("symbol") === symbol;
  }) as YAML.YAMLMap | undefined;

  if (!symItem) return doc.toString();

  if (updates.leverage !== undefined && venueName !== "binance_spot") {
    symItem.set("leverage", updates.leverage);
  }
  if (updates.strategies !== undefined) {
    if (updates.strategies.length === 0) {
      symItem.set("strategies", doc.createNode([]));
    } else {
      symItem.set("strategies", doc.createNode(updates.strategies));
    }
  }

  return doc.toString();
}
