# ArbiTron

**ArbiTron** is a fast and lightweight arbitrage bot designed to take advantage of price differences between cryptocurrency exchanges.

> ⚡ Built for speed. Optimized for opportunities. Trusted by traders.

---

## 🚀 Features

- 📈 **Real-time market monitoring**  
  Continuously tracks price differences across multiple exchanges.

- 🤖 **Automated trading logic**  
  Executes buy/sell orders when profitable arbitrage is detected.

- 🔒 **Secure and modular**  
  Easily configurable API keys and exchange connectors.

- 📊 **Logging and analytics**  
  Logs every arbitrage opportunity and executed trade for later analysis.

---

## 📦 Installation

Clone the project:

```bash
git clone https://github.com/raykavin/ArbiTron.git
cd ArbiTron
```

Build the project:

```bash
go build -o arbitron ./cmd
```

---

## ⚙️ Configuration

Before running the bot, you need to set up your exchange API credentials and configuration file.

Edit the configuration file (e.g. `config.yaml`) with:

```yaml
min_spread_percentage: 0.2
min_profit_percentage: 0.15
min_profit_amount: 0.5
use_mainnet: false
order_book_depth: 50
max_stale_duration: 30s
check_interval: 500ms
update_ui_interval: 300ms
quoted_asset: "USDT"
coins:
  - "BTC"
  - "ETH"
  - "SOL"
  - "ATOM"
  - "IOTA"
trade_fees:
    Hyperliquid: 2
    KuCoin: 2
    Binance: 2
    OKX: 2
    Bybit: 2
    MEXC: 2
```

---

## 🛠️ Usage

Run the bot using:

```bash
./arbitron
```

By default, the bot will log opportunities and execute trades when the profit threshold is reached.


## 📌 Roadmap

- [x] Multi-exchange support  
- [ ] Simulated trading (paper mode)  
- [ ] Web dashboard for live tracking  
- [ ] Telegram/Slack notifications  
- [ ] Strategy plugins

---

## 🤝 Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change or add.

---

## 📄 License

MIT License © [Raykavin Meireles](https://github.com/raykavin)

---

## 📬 Contact

Feel free to reach out for support or collaboration:  
**Email**: raykavin.meireles@gmail.com  
**GitHub**: [@raykavin](https://github.com/raykavin)