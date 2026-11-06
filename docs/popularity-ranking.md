# Popularity Ranking & Hotness Algorithm Specification

This document presents the mathematical derivations, literature survey, failure mode analysis, and empirical benchmarks on the 100,000-post `hypnoview.db` database that justify the production popularity ranking algorithm used in `hypnoview`.

---

## 1. Executive Summary

Popularity ranking on a booru differs fundamentally from news aggregators like Hacker News or Reddit. On Hypnohub:
1. **Engagement signals are scarce**: There are **no view counts**; `score` (net upvotes/favorites) is the sole engagement metric available.
2. **Exposure is front-loaded**: A post receives 80–90% of its lifetime upvotes during its first 12–24 hours while on Page 1 (`pid=0`) of the newest feed. Once displaced by newer uploads, organic impressions drop to near zero.
3. **Users browse via explicit sliding windows**: Users switch between `6h` (Rising), `24h` (Today), `3d` (3 Days), `7d` (Week), `30d` (Month), and `all` (All Indexed).

Ranking by raw score ($\Delta S$) biases heavily toward older posts that had more exposure time. Conversely, ranking by raw velocity ($\Delta S / \Delta t$) or sublinear time decay ($(\Delta S + 0.5) / (\Delta t + 2)^{1.35}$) results in catastrophic scale invariance and recency bias, allowing low-scoring 1–2 hour posts to crowd out viral multi-day posts in weekly, monthly, and all-time windows.

The production formula adopted by `hypnoview` is **Superlinear Momentum Gravity ($F2$) with Window-Proportional Exposure Damping ($\tau_W$)**:

$$\text{HotScore}(p, W) = \frac{\Delta S}{\Delta t + \tau_W} \cdot \max(1, S)^{0.35}$$

Where:
- $\Delta S$: Upvotes gained within the sliding window $W$.
- $S$: Total cumulative score of the post.
- $\Delta t$: Effective exposure hours of the post inside the window ($\min(W, \text{age})$).
- $\tau_W$: Window-proportional damping constant scaling from $1.5\text{h}$ for `6h` up to $168.0\text{h}$ for `all`.

---

## 2. Problem Statement: Why Pure Velocity ($\Delta S / \Delta t$) Fails

### 2.1 Scale Invariance

Under pure velocity $v = \frac{\Delta S}{\Delta t}$, rate is completely scale-invariant with respect to time:

$$\frac{2\text{ votes}}{1\text{ hour}} = \frac{48\text{ votes}}{24\text{ hours}} = \frac{336\text{ votes}}{168\text{ hours}} = 2.0\text{ votes/hr}$$

In practice, sustaining an upvote rate over 24 hours or 7 days is vastly more difficult than experiencing a random 1-hour burst. Furthermore:
- A 30-minute-old post with 3 votes achieves $v = \frac{3}{0.5} = 6.0/\text{h}$.
- A 21-hour-old post with 100 votes achieves $v = \frac{100}{21} = 4.76/\text{h}$.

Under raw velocity, the 3-vote post beats the 100-vote viral hit. Under a Poisson process $\Delta S \sim \text{Poisson}(\lambda \Delta t)$, the variance of the rate estimator is $\operatorname{Var}(\hat{\lambda}) = \frac{\lambda}{\Delta t}$. When $\Delta t \to 0$, rate estimates fluctuate wildly, causing the top ranks to be dominated by noise.

### 2.2 The Sublinear Time Decay Trap ($g > p$)

A common heuristic to tame small-$\Delta t$ spikes is to introduce power-law time decay:

$$\text{Score}(p) = \frac{\Delta S + c}{(\Delta t + \tau)^g}$$

If $g > 1.0$ while the score exponent $p = 1.0$ (for example, $\frac{\Delta S + 0.5}{(\Delta t + 2)^{1.35}}$ where $g = 1.35$ and $p = 1.0$), we can decompose the equation by factoring out constant velocity $v = \frac{\Delta S}{\Delta t}$:

$$\frac{\Delta S}{(\Delta t + \tau)^{1.35}} \approx \frac{v \cdot \Delta t}{\Delta t^{1.35}} = v \cdot \Delta t^{-0.35}$$

Because the exponent on time is negative ($-0.35$), **any post that maintains a steady upvote velocity $v$ suffers continuous decay over time**:

| Post Duration | Total Upvotes ($\Delta S$) | Average Rate ($v$) | Score ($\frac{\Delta S + 0.5}{(\Delta t + 2)^{1.35}}$) | Penalty Relative to 2h |
|---|---|---|---|---|
| **2 hours** | 10 | $5.0/\text{h}$ | $\frac{10.5}{4^{1.35}} = \mathbf{1.616}$ | Baseline ($1.00\times$) |
| **8 hours** | 40 | $5.0/\text{h}$ | $\frac{40.5}{10^{1.35}} = \mathbf{1.810}$ | $+12\%$ (near plateau) |
| **20 hours** | 100 | $5.0/\text{h}$ | $\frac{100.5}{22^{1.35}} = \mathbf{1.547}$ | **$-4\%$ (punished for surviving)** |
| **48 hours** | 240 | $5.0/\text{h}$ | $\frac{240.5}{50^{1.35}} = \mathbf{1.222}$ | **$-24\%$ (severely suppressed)** |

Setting $g > p$ rewards brief 1–2 hour spikes and penalizes posts that sustain popularity. On `hypnoview.db`, this formula causes Post `#287424` (100 votes in 21.2h, $4.71/\text{h}$, the #1 post of the day) to drop to **#7**, losing to Post `#287527` (16 votes, 3.9h) and Post `#287547` (8 votes, 2.0h).

### 2.3 Fixed Damping $\tau = 2\text{h}$ Across Multi-Day Windows

Exposure damping $\frac{\Delta S}{\Delta t + \tau}$ introduces a pseudo-prior $\tau$ representing the baseline observation window. If $\tau = 2.0\text{h}$ is kept constant across multi-day windows:
- In `24h`, $\tau = 2\text{h}$ represents $\frac{2}{24} \approx 8.3\%$ of the window.
- In `7d`, $\tau = 2\text{h}$ is only $\frac{2}{168} \approx 1.2\%$ of the window.
- In `30d`, $\tau = 2\text{h}$ is $\frac{2}{720} \approx 0.28\%$ of the window.

Because booru posts stop receiving organic votes after leaving Page 1 (~24–48 hours after upload), an older post's score $\Delta S$ freezes around hour 40 while its denominator $\Delta t + 2\text{h}$ continues growing to $168\text{h}$ or $720\text{h}$. 

Consequently, a 5-day-old post with 220 votes has a damped rate of $\frac{220}{120 + 2} = 1.80/\text{h}$, whereas a brand-new 7-hour post with only 40 votes achieves $\frac{40}{7 + 2} = 4.44/\text{h}$. A fixed $\tau = 2\text{h}$ ensures that **the top of `7d`, `30d`, and `all` is permanently dominated by recent 8-to-24 hour posts**, rendering multi-day windows ineffective.

---

## 3. Literature Survey of Existing Ranking Algorithms

### 3.1 Hacker News Gravity

Hacker News ranks submissions using the power-law gravity equation:

$$\text{Score}_{\text{HN}} = \frac{(P - 1)^p}{(T + 2)^G}, \quad p = 0.8, \; G = 1.8$$

Where $P$ is points and $T$ is time in hours since submission. Rewriting this in terms of damped velocity $v = \frac{P}{T + 2}$:

$$\text{Score}_{\text{HN}} = \left(\frac{P}{T + 2}\right) \cdot \frac{P^{-0.2}}{(T + 2)^{0.8}}$$

HN uses $p = 0.8 < 1.0$ (sublinear score) and $G = 1.8 > 1.0$ (superlinear age decay) for a specific reason: HN operates a **single global front page with no time window filters**. To prevent viral submissions from lingering indefinitely on the front page, HN applies an aggressive time penalty ($G = 1.8$).

In `hypnoview`, time filtering is handled explicitly by sliding window selections (`24h`, `7d`, `30d`). When a user chooses "This Week", they want to find the best posts of the week, not have week-old posts crushed by $T^{1.8}$ decay.

### 3.2 Reddit "Hot" & Danbooru / Lemmy Log-Decay

#### Reddit "Hot"
$$\text{Score}_{\text{Reddit}} = \log_{10}(\max(1, |S|)) + \operatorname{sign}(S) \cdot \frac{t_{\text{created}} - t_0}{45{,}000}$$

Every $45{,}000\text{ seconds}$ ($12.5\text{ hours}$), a post requires a $10\times$ increase in votes to preserve its position. This is equivalent to exponential decay with a fixed half-life.

#### Danbooru `order:rank`
Danbooru (`app/models/post.rb`) filters posts to the last 2 days (`created_at >= 2.days.ago`) and ranks by:

$$\text{Rank}_{\text{Danbooru}} = \log_3(S) + \frac{t_{\text{created}} - t_0}{35{,}000}$$

This implements exponential decay with a half-life of $\frac{35{,}000 \ln 2}{\ln 3} \approx 6.13\text{ hours}$.

#### Lemmy "Hot"
$$\text{Rank}_{\text{Lemmy}} = \frac{10{,}000 \cdot \ln(\max(1, 3 + S))}{(T + 2)^{1.8}}$$

**Limitation for Multi-Window Browsing**: Exponential half-lives and logarithmic scores enforce a steep drop-off. If tuned to keep 24-hour feeds fresh ($H \approx 12\text{h}$), any post older than 36 hours is penalized into obscurity. If tuned to 7 days, the 24-hour feed becomes static.

### 3.3 Bayesian Shrinkage Rate (Empirical Bayes / Gamma-Poisson)

Modeling upvote arrivals as a Poisson process with rate $\lambda$ and a conjugate $\text{Gamma}(\alpha, \beta)$ prior yields the posterior mean rate:

$$\hat{\lambda}_{\text{Bayes}} = \frac{\Delta S + \alpha}{\Delta t + \beta} = \frac{\Delta S + m \cdot r_0}{\Delta t + m}$$

Where $r_0 = \frac{\alpha}{\beta}$ is the prior background arrival rate (e.g., $0.5\text{ votes/hr}$) and $m = \beta$ is the pseudo-exposure weight (damping parameter).

While Bayesian shrinkage eliminates small-sample noise for $\Delta t < m$, it remains **scale-invariant** as soon as $\Delta t \gg m$:

$$\lim_{\Delta t \gg m} \hat{\lambda}_{\text{Bayes}} = \frac{\Delta S}{\Delta t}$$

It does not address the fundamental issue that sustaining $5/\text{h}$ over 24 hours reflects greater popularity than sustaining $5/\text{h}$ over 2 hours.

### 3.4 Physics Momentum / Work-Energy Ranking ($\text{Mass} \times \text{Velocity}$)

In classical mechanics, kinetic energy is $E_k = \frac{1}{2} m v^2$ and momentum is $p = m v$. If we model a post's popularity as a physical projectile:
- **Velocity** ($v = \frac{\Delta S}{\Delta t + \tau_W}$): Current rate of upvote accumulation.
- **Mass** ($M = S^\beta$): Total accumulated upvote mass.

Multiplying velocity by mass yields momentum:

$$\text{Popularity} = \text{Velocity} \times \text{Mass} = \left(\frac{\Delta S}{\Delta t + \tau_W}\right) \cdot S^\beta = \frac{\Delta S \cdot S^\beta}{\Delta t + \tau_W}$$

When $\Delta S \approx S$ (posts created inside the window), this simplifies to:

$$\frac{S^{1 + \beta}}{\Delta t + \tau_W}$$

With $\beta = 0.35$, the effective score exponent is $p = 1.35$, while the time exponent is $g = 1.0$. Because $p > g$, sustained upvote velocity is rewarded superlinearly over brief bursts.

---

## 4. Candidate Formulas Derived & Tested

Six candidate formulas were formulated and evaluated:

### Formula 1 (F1): Old Baseline / Sublinear Gravity Trap ($p = 1.0, g = 1.35$)
$$\text{Score}_{F1} = \frac{\Delta S + 0.5}{(\Delta t + 2.0)^{1.35}}$$
- **SQLite Expression**:
  ```sql
  (score_delta + 0.5) / POWER(eff_hours + 2.0, 1.35)
  ```
- **Properties**: Punishes posts as they age ($v \cdot \Delta t^{-0.35}$). Promotes 1–2 hour posts with few votes over sustained high-performing posts.

### Raw Velocity ($v = \Delta S / \Delta t$)
$$v = \frac{\Delta S}{\max(0.1, \Delta t)}$$
- **SQLite Expression**:
  ```sql
  CAST(score_delta AS REAL) / MAX(0.1, eff_hours)
  ```
- **Properties**: Scale-invariant rate. Unstable at small $\Delta t$.

### Formula 2 (F2): Superlinear Momentum Gravity ($p = 1.35, g = 1.0$)
$$\text{HotScore}_{F2} = \left(\frac{\Delta S}{\Delta t + \tau_W}\right) \cdot \max(1, S)^{0.35}$$
- **SQLite Expression**:
  ```sql
  (CAST(score_delta AS REAL) / (eff_hours + :tau_W)) * POWER(MAX(1, p.score), 0.35)
  ```
- **Properties**: Combines damped window velocity with total score magnitude. Rewards sustained rate ($p = 1.35 > g = 1.0$) and scales across both short and multi-day windows via window-proportional damping $\tau_W$.

### Formula 3 (F3): Hacker News Classic Gravity ($p = 0.8, G = 1.8$)
$$\text{Score}_{F3} = \frac{\max(0, \Delta S - 1)^{0.8}}{(\Delta t + 2.0)^{1.8}}$$
- **SQLite Expression**:
  ```sql
  POWER(MAX(0.0, score_delta - 1), 0.8) / POWER(eff_hours + 2.0, 1.8)
  ```
- **Properties**: Heavy age suppression designed for a single un-windowed feed. Over-penalizes posts older than 12 hours.

### Formula 4 (F4): Danbooru / Reddit Log-Decay Hybrid
$$\text{Score}_{F4} = \log_{10}(\max(1, \Delta S)) + 0.5 \log_{10}(\max(1, S)) - \frac{\Delta t}{H_W}$$
- **SQLite Expression**:
  ```sql
  LOG10(MAX(1, score_delta)) + 0.5 * LOG10(MAX(1, p.score)) - (eff_hours / :H_W)
  ```
- **Properties**: Linear decay on a logarithmic scale with window horizon $H_W$ ($24\text{h}$, $168\text{h}$, $720\text{h}$, $2160\text{h}$).

### Formula 5 (F5): Bayesian Shrinkage Rate ($m = 2.0\text{h}, r_0 = 0.5/\text{h}$)
$$\text{Score}_{F5} = \frac{\Delta S + m \cdot r_0}{\Delta t + m}$$
- **SQLite Expression**:
  ```sql
  (CAST(score_delta AS REAL) + 1.0) / (eff_hours + 2.0)
  ```
- **Properties**: Smooths small-sample rate spikes but behaves identically to linear velocity once $\Delta t \gg 2\text{h}$.

### Formula 6 (F6): Log-Mass Bayesian Momentum
$$\text{Score}_{F6} = \left(\frac{\Delta S + \tau_W \cdot r_0}{\Delta t + \tau_W}\right) \cdot \ln(1 + S)$$
- **SQLite Expression**:
  ```sql
  ((CAST(score_delta AS REAL) + :tau_W * 0.5) / (eff_hours + :tau_W)) * LN(1.0 + p.score)
  ```
- **Properties**: Shrinks small-sample rate, scaled by log cumulative score.

---

## 5. Empirical Benchmark on `hypnoview.db` (100,000 Seeded Posts)

### 5.1 Dataset Overview

The benchmark was executed against `/home/diamond/Scripts/diamondburned/hypnoview/hypnoview.db`:
- **Total Posts**: 100,000 (IDs `180071` to `287547`)
- **Active Posts**: 99,790
- **Date Range**: `2023-08-30 11:40:10 UTC` to `2026-11-05 22:50:12 UTC` (3.2 years)
- **Reference Evaluation Time ($T_{\text{ref}}$)**: `1793926231` (`2026-11-06 00:50:31 UTC`, latest poll timestamp)

#### Score Distribution Percentiles

| Metric | Posts Analyzed | Min | $P_{25}$ | Median ($P_{50}$) | Mean | $P_{75}$ | $P_{90}$ | $P_{95}$ | $P_{99}$ | Max |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| **All Time (`all`)** | 100,000 | 0 | 15 | 36 | 55.38 | 76 | 131 | 173 | 269 | 6,049 |
| **30 Days (`30d`)** | 2,707 | 0 | 9 | 23 | 36.95 | 53 | 89 | 114 | 179 | 318 |
| **7 Days (`7d`)** | 785 | 0 | 5 | 16 | 27.04 | 37 | 66 | 95 | 138 | 220 |
| **24 Hours (`24h`)** | 124 | 0 | 1 | 5 | 11.82 | 15 | 29 | 46 | 63 | 100 |
| **6 Hours (`6h`)** | 62 | 1 | 1 | 2 | 3.40 | 5 | 7 | 14 | 27 | 29 |

---

### 5.2 24-Hour Window (`Today`)

Window parameters: $W = 24\text{h}$, $\tau_W = 2.0\text{h}$, $H_W = 24\text{h}$. 124 active posts eligible.

#### Top 10 Side-by-Side Comparison

| Rank | F1 (Old Sublinear) | Raw Velocity ($\Delta S / \Delta t$) | F2 (Superlinear Momentum) | F3 (HN Gravity) | F4 (Log-Decay) | F6 (Log-Mass) |
|---:|---|---|---|---|---|---|
| **#1** | `#287458` ($S=42, 7.5\text{h}$) **`2.03`** | `#287458` ($S=42, 7.5\text{h}$) **`5.58`** | `#287424` ($S=100, 21.2\text{h}$) **`21.58`** | `#287547` ($S=8, 2.0\text{h}$) **`0.39`** | `#287450` ($S=46, 8.3\text{h}$) **`2.15`** | `#287424` ($S=100, 21.2\text{h}$) **`20.07`** |
| **#2** | `#287450` ($S=46, 8.3\text{h}$) **`1.99`** | `#287450` ($S=46, 8.3\text{h}$) **`5.52`** | `#287450` ($S=46, 8.3\text{h}$) **`17.00`** | `#287525` ($S=27, 5.2\text{h}$) **`0.38`** | `#287458` ($S=42, 7.5\text{h}$) **`2.12`** | `#287450` ($S=46, 8.3\text{h}$) **`17.51`** |
| **#3** | `#287525` ($S=27, 5.2\text{h}$) **`1.90`** | `#287525` ($S=27, 5.2\text{h}$) **`5.16`** | `#287458` ($S=42, 7.5\text{h}$) **`16.31`** | `#287517` ($S=29, 5.8\text{h}$) **`0.36`** | `#287424` ($S=100, 21.2\text{h}$) **`2.12`** | `#287458` ($S=42, 7.5\text{h}$) **`16.97`** |
| **#4** | `#287517` ($S=29, 5.8\text{h}$) **`1.86`** | `#287517` ($S=29, 5.8\text{h}$) **`5.04`** | `#287440` ($S=63, 14.8\text{h}$) **`16.02`** | `#287527` ($S=16, 3.9\text{h}$) **`0.36`** | `#287440` ($S=63, 14.8\text{h}$) **`2.08`** | `#287440` ($S=63, 14.8\text{h}$) **`15.88`** |
| **#5** | `#287474` ($S=26, 6.1\text{h}$) **`1.58`** | `#287424` ($S=100, 21.2\text{h}$) **`4.71`** | `#287429` ($S=72, 19.3\text{h}$) **`15.14`** | `#287458` ($S=42, 7.5\text{h}$) **`0.34`** | `#287429` ($S=72, 19.3\text{h}$) **`1.98`** | `#287429` ($S=72, 19.3\text{h}$) **`14.74`** |
| **#6** | `#287527` ($S=16, 3.9\text{h}$) **`1.51`** | `#287474` ($S=26, 6.1\text{h}$) **`4.29`** | `#287517` ($S=29, 5.8\text{h}$) **`12.16`** | `#287450` ($S=46, 8.3\text{h}$) **`0.31`** | `#287517` ($S=29, 5.8\text{h}$) **`1.95`** | `#287517` ($S=29, 5.8\text{h}$) **`13.16`** |
| **#7** | `#287424` ($S=100, 21.2\text{h}$) **`1.44`** | `#287440` ($S=63, 14.8\text{h}$) **`4.27`** | `#287525` ($S=27, 5.2\text{h}$) **`11.83`** | `#287474` ($S=26, 6.1\text{h}$) **`0.31`** | `#287525` ($S=27, 5.2\text{h}$) **`1.93`** | `#287525` ($S=27, 5.2\text{h}$) **`12.89`** |
| **#8** | `#287440` ($S=63, 14.8\text{h}$) **`1.41`** | `#287527` ($S=16, 3.9\text{h}$) **`4.12`** | `#287423` ($S=62, 21.5\text{h}$) **`11.18`** | `#287462` ($S=20, 6.9\text{h}$) **`0.21`** | `#287474` ($S=26, 6.1\text{h}$) **`1.87`** | `#287435` ($S=56, 18.5\text{h}$) **`11.24`** |
| **#9** | `#287547` ($S=8, 2.0\text{h}$) **`1.31`** | `#287547` ($S=8, 2.0\text{h}$) **`3.99`** | `#287435` ($S=56, 18.5\text{h}$) **`11.17`** | `#287521` ($S=14, 5.6\text{h}$) **`0.20`** | `#287435` ($S=56, 18.5\text{h}$) **`1.85`** | `#287423` ($S=62, 21.5\text{h}$) **`11.10`** |
| **#10** | `#287429` ($S=72, 19.3\text{h}$) **`1.17`** | `#287429` ($S=72, 19.3\text{h}$) **`3.74`** | `#287419` ($S=61, 22.1\text{h}$) **`10.69`** | `#287469` ($S=17, 6.4\text{h}$) **`0.20`** | `#287441` ($S=39, 13.3\text{h}$) **`1.83`** | `#287474` ($S=26, 6.1\text{h}$) **`11.04`** |

#### Analysis:
- **F1 Failure**: Post `#287424` maintained $4.71\text{ votes/hr}$ for 21.2 hours to reach 100 points ($P_{99}$ for the day). Under F1, it was pushed down to **#7**, beaten by 16-point (`#287527`) and 26-point (`#287474`) posts. F1 also placed an 8-point post (`#287547`) at #9.
- **F3 Failure**: Classic HN gravity ranked `#287547` ($S=8$, age $2.0\text{h}$) at **#1** for the entire day.
- **F2 Success**: Post `#287424` cleanly takes **#1** (`21.58`). Explosive breakout posts `#287450` ($S=46$ in 8.3h, $5.52/\text{h}$) and `#287458` ($S=42$ in 7.5h, $5.58/\text{h}$) take **#2 and #3**, beating slower older posts (`#287429` $S=72$, `#287423` $S=62$). Fast-rising 5-hour posts `#287517` ($S=29$, $5.04/\text{h}$) and `#287525` ($S=27$, $5.16/\text{h}$) take **#6 and #7**.

---

### 5.3 7-Day Window (`Week`)

Window parameters: $W = 168\text{h}$, $\tau_W = 12.0\text{h}$, $H_W = 168\text{h}$. 785 active posts eligible.

#### Top 10 Side-by-Side Comparison

| Rank | F1 (Old Sublinear) | Raw Velocity ($\Delta S / \Delta t$) | F2 (Superlinear Momentum) | F3 (HN Gravity) | F4 (Log-Decay) | F6 (Log-Mass) |
|---:|---|---|---|---|---|---|
| **#1** | `#287458` ($S=42, 7.5\text{h}$) **`2.03`** | `#287376` ($S=173, 29.8\text{h}$) **`5.81`** | `#287376` ($S=173, 29.8\text{h}$) **`25.15`** | `#287547` ($S=8, 2.0\text{h}$) **`0.39`** | `#287376` ($S=173, 29.8\text{h}$) **`3.18`** | `#287376` ($S=173, 29.8\text{h}$) **`22.11`** |
| **#2** | `#287450` ($S=46, 8.3\text{h}$) **`1.99`** | `#287458` ($S=42, 7.5\text{h}$) **`5.58`** | `#287316` ($S=170, 42.7\text{h}$) **`18.75`** | `#287525` ($S=27, 5.2\text{h}$) **`0.38`** | `#287316` ($S=170, 42.7\text{h}$) **`3.09`** | `#287316` ($S=170, 42.7\text{h}$) **`16.54`** |
| **#3** | `#287525` ($S=27, 5.2\text{h}$) **`1.90`** | `#287450` ($S=46, 8.3\text{h}$) **`5.52`** | `#287424` ($S=100, 21.2\text{h}$) **`15.08`** | `#287517` ($S=29, 5.8\text{h}$) **`0.36`** | `#287317` ($S=136, 42.6\text{h}$) **`2.95`** | `#287424` ($S=100, 21.2\text{h}$) **`14.72`** |
| **#4** | `#287517` ($S=29, 5.8\text{h}$) **`1.86`** | `#287525` ($S=27, 5.2\text{h}$) **`5.16`** | `#287381` ($S=117, 29.3\text{h}$) **`14.99`** | `#287527` ($S=16, 3.9\text{h}$) **`0.36`** | `#287224` ($S=166, 66.6\text{h}$) **`2.93`** | `#287381` ($S=117, 29.3\text{h}$) **`14.20`** |
| **#5** | `#287376` ($S=173, 29.8\text{h}$) **`1.63`** | `#287517` ($S=29, 5.8\text{h}$) **`5.04`** | `#287317` ($S=136, 42.6\text{h}$) **`13.89`** | `#287458` ($S=42, 7.5\text{h}$) **`0.34`** | `#287381` ($S=117, 29.3\text{h}$) **`2.93`** | `#287317` ($S=136, 42.6\text{h}$) **`12.79`** |
| **#6** | `#287474` ($S=26, 6.1\text{h}$) **`1.58`** | `#287424` ($S=100, 21.2\text{h}$) **`4.71`** | `#287224` ($S=166, 66.6\text{h}$) **`12.63`** | `#287450` ($S=46, 8.3\text{h}$) **`0.31`** | `#287424` ($S=100, 21.2\text{h}$) **`2.87`** | `#287394` ($S=95, 27.3\text{h}$) **`11.74`** |
| **#7** | `#287527` ($S=16, 3.9\text{h}$) **`1.51`** | `#287474` ($S=26, 6.1\text{h}$) **`4.29`** | `#287394` ($S=95, 27.3\text{h}$) **`11.90`** | `#287474` ($S=26, 6.1\text{h}$) **`0.31`** | `#287328` ($S=112, 39.5\text{h}$) **`2.84`** | `#287224` ($S=166, 66.6\text{h}$) **`11.19`** |
| **#8** | `#287424` ($S=100, 21.2\text{h}$) **`1.44`** | `#287440` ($S=63, 14.8\text{h}$) **`4.27`** | `#287328` ($S=112, 39.5\text{h}$) **`11.33`** | `#287462` ($S=20, 6.9\text{h}$) **`0.21`** | `#287394` ($S=95, 27.3\text{h}$) **`2.80`** | `#287328` ($S=112, 39.5\text{h}$) **`10.82`** |
| **#9** | `#287440` ($S=63, 14.8\text{h}$) **`1.41`** | `#287527` ($S=16, 3.9\text{h}$) **`4.12`** | `#287377` ($S=91, 29.7\text{h}$) **`10.57`** | `#287521` ($S=14, 5.6\text{h}$) **`0.20`** | `#286998` ($S=201, 110.9\text{h}$) **`2.79`** | `#287440` ($S=63, 14.8\text{h}$) **`10.72`** |
| **#10** | `#287547` ($S=8, 2.0\text{h}$) **`1.31`** | `#287547` ($S=8, 2.0\text{h}$) **`3.99`** | `#287405` ($S=84, 25.8\text{h}$) **`10.47`** | `#287469` ($S=17, 6.4\text{h}$) **`0.20`** | `#287223` ($S=133, 66.7\text{h}$) **`2.79`** | `#287429` ($S=72, 19.3\text{h}$) **`10.71`** |

#### Analysis:
- **F1 / Raw Velocity Failure**: In both F1 and Raw Velocity, the Top 4 posts of the **entire week** are the exact same 5-to-8 hour posts from today (`#287458` $S=42$, `#287450` $S=46$, `#287525` $S=27$, `#287517` $S=29$). In F1, an 8-point post (`#287547`) places in the Top 10 of the week.
- **F2 Success**: With $\tau_W = 12.0\text{h}$, F2 accurately surfaces the week's major viral sensations: `#287376` ($S=173$, 29.8h, `25.15`) and `#287316` ($S=170$, 42.7h, `18.75`) lead the rankings, followed by `#287424` ($S=100$), `#287381` ($S=117$), `#287317` ($S=136$), and `#287224` ($S=166$, 2.8 days old).

---

### 5.4 30-Day Window (`Month`)

Window parameters: $W = 720\text{h}$, $\tau_W = 48.0\text{h}$, $H_W = 720\text{h}$. 2,707 active posts eligible.

#### Top 10 Side-by-Side Comparison

| Rank | F1 (Old Sublinear) | Raw Velocity ($\Delta S / \Delta t$) | F2 (Superlinear Momentum) | F3 (HN Gravity) | F4 (Log-Decay) | F6 (Log-Mass) |
|---:|---|---|---|---|---|---|
| **#1** | `#287458` ($S=42, 7.5\text{h}$) **`2.03`** | `#287376` ($S=173, 29.8\text{h}$) **`5.81`** | `#287376` ($S=173, 29.8\text{h}$) **`13.51`** | `#287547` ($S=8, 2.0\text{h}$) **`0.39`** | `#287376` ($S=173, 29.8\text{h}$) **`3.32`** | `#287376` ($S=173, 29.8\text{h}$) **`13.07`** |
| **#2** | `#287450` ($S=46, 8.3\text{h}$) **`1.99`** | `#287458` ($S=42, 7.5\text{h}$) **`5.58`** | `#287316` ($S=170, 42.7\text{h}$) **`11.31`** | `#287525` ($S=27, 5.2\text{h}$) **`0.38`** | `#286729` ($S=220, 152.2\text{h}$) **`3.30`** | `#287316` ($S=170, 42.7\text{h}$) **`11.00`** |
| **#3** | `#287525` ($S=27, 5.2\text{h}$) **`1.90`** | `#287450` ($S=46, 8.3\text{h}$) **`5.52`** | `#287224` ($S=166, 66.6\text{h}$) **`8.67`** | `#287517` ($S=29, 5.8\text{h}$) **`0.36`** | `#286998` ($S=201, 110.9\text{h}$) **`3.30`** | `#287381` ($S=117, 29.3\text{h}$) **`8.70`** |
| **#4** | `#287517` ($S=29, 5.8\text{h}$) **`1.86`** | `#287525` ($S=27, 5.2\text{h}$) **`5.16`** | `#287317` ($S=136, 42.6\text{h}$) **`8.38`** | `#287527` ($S=16, 3.9\text{h}$) **`0.36`** | `#287316` ($S=170, 42.7\text{h}$) **`3.29`** | `#287317` ($S=136, 42.6\text{h}$) **`8.69`** |
| **#5** | `#287376` ($S=173, 29.8\text{h}$) **`1.63`** | `#287517` ($S=29, 5.8\text{h}$) **`5.04`** | `#286998` ($S=201, 110.9\text{h}$) **`8.10`** | `#287458` ($S=42, 7.5\text{h}$) **`0.34`** | `#286916` ($S=193, 123.2\text{h}$) **`3.26`** | `#287224` ($S=166, 66.6\text{h}$) **`8.48`** |
| **#6** | `#287474` ($S=26, 6.1\text{h}$) **`1.58`** | `#287424` ($S=100, 21.2\text{h}$) **`4.71`** | `#287381` ($S=117, 29.3\text{h}$) **`8.01`** | `#287450` ($S=46, 8.3\text{h}$) **`0.31`** | `#287224` ($S=166, 66.6\text{h}$) **`3.24`** | `#287424` ($S=100, 21.2\text{h}$) **`8.27`** |
| **#7** | `#287527` ($S=16, 3.9\text{h}$) **`1.51`** | `#287474` ($S=26, 6.1\text{h}$) **`4.29`** | `#286729` ($S=220, 152.2\text{h}$) **`7.26`** | `#287474` ($S=26, 6.1\text{h}$) **`0.31`** | `#286052` ($S=290, 340.0\text{h}$) **`3.22`** | `#286998` ($S=201, 110.9\text{h}$) **`7.52`** |
| **#8** | `#287424` ($S=100, 21.2\text{h}$) **`1.44`** | `#287440` ($S=63, 14.8\text{h}$) **`4.27`** | `#287424` ($S=100, 21.2\text{h}$) **`7.24`** | `#287462` ($S=20, 6.9\text{h}$) **`0.21`** | `#286638` ($S=202, 187.3\text{h}$) **`3.20`** | `#287328` ($S=112, 39.5\text{h}$) **`7.34`** |
| **#9** | `#287440` ($S=63, 14.8\text{h}$) **`1.41`** | `#287527` ($S=16, 3.9\text{h}$) **`4.12`** | `#286916` ($S=193, 123.2\text{h}$) **`7.11`** | `#287521` ($S=14, 5.6\text{h}$) **`0.20`** | `#286493` ($S=203, 201.7\text{h}$) **`3.18`** | `#287394` ($S=95, 27.3\text{h}$) **`7.21`** |
| **#10** | `#287547` ($S=8, 2.0\text{h}$) **`1.31`** | `#287547` ($S=8, 2.0\text{h}$) **`3.99`** | `#287328` ($S=112, 39.5\text{h}$) **`6.67`** | `#287469` ($S=17, 6.4\text{h}$) **`0.20`** | `#287317` ($S=136, 42.6\text{h}$) **`3.14`** | `#287223` ($S=133, 66.7\text{h}$) **`6.70`** |

#### Analysis:
- **F1 Breakdown**: In F1, the Top 4 of the entire month are still the same four posts from today, and 8-point `#287547` remains at #10. The month's highest-scoring post (`#286729`, $S=220$, 6.3 days old) does not even appear in F1's Top 50.
- **F2 Success**: With $\tau_W = 48.0\text{h}$, F2 seamlessly balances 1–3 day posts (`#287376` $S=173$, `#287316` $S=170$, `#287224` $S=166$) with 4–7 day viral hits (`#286998` $S=201$ at 4.6d, `#286729` $S=220$ at 6.3d, `#286916` $S=193$ at 5.1d).

---

### 5.5 All-Time Window (`All Indexed`)

Window parameters: $W = 0$ (unbounded), $\tau_W = 168.0\text{h}$, $H_W = 2160\text{h}$. 99,790 active posts eligible.

#### Top 10 Side-by-Side Comparison

| Rank | F1 (Old Sublinear) | Raw Velocity ($\Delta S / \Delta t$) | F2 (Superlinear Momentum) | F3 (HN Gravity) | F4 (Log-Decay) | F6 (Log-Mass) |
|---:|---|---|---|---|---|---|
| **#1** | `#287458` ($S=42, 7.5\text{h}$) **`2.03`** | `#287376` ($S=173, 29.8\text{h}$) **`5.81`** | `#204959` ($S=6049, 2.5\text{y}$) **`5.76`** | `#287547` ($S=8, 2.0\text{h}$) **`0.39`** | `#286052` ($S=290, 14.2\text{d}$) **`3.54`** | `#287376` ($S=173, 29.8\text{h}$) **`6.70`** |
| **#2** | `#287450` ($S=46, 8.3\text{h}$) **`1.99`** | `#287458` ($S=42, 7.5\text{h}$) **`5.58`** | `#287376` ($S=173, 29.8\text{h}$) **`5.31`** | `#287525` ($S=27, 5.2\text{h}$) **`0.38`** | `#286729` ($S=220, 6.3\text{d}$) **`3.44`** | `#287316` ($S=170, 42.7\text{h}$) **`6.20`** |
| **#3** | `#287525` ($S=27, 5.2\text{h}$) **`1.90`** | `#287450` ($S=46, 8.3\text{h}$) **`5.52`** | `#287316` ($S=170, 42.7\text{h}$) **`4.87`** | `#287517` ($S=29, 5.8\text{h}$) **`0.36`** | `#284694` ($S=318, 29.2\text{d}$) **`3.43`** | `#287224` ($S=166, 2.8\text{d}$) **`5.45`** |
| **#4** | `#287517` ($S=29, 5.8\text{h}$) **`1.86`** | `#287525` ($S=27, 5.2\text{h}$) **`5.16`** | `#286998` ($S=201, 4.6\text{d}$) **`4.61`** | `#287527` ($S=16, 3.9\text{h}$) **`0.36`** | `#286998` ($S=201, 4.6\text{d}$) **`3.40`** | `#286998` ($S=201, 4.6\text{d}$) **`5.42`** |
| **#5** | `#287376` ($S=173, 29.8\text{h}$) **`1.63`** | `#287517` ($S=29, 5.8\text{h}$) **`5.04`** | `#286729` ($S=220, 6.3\text{d}$) **`4.54`** | `#287458` ($S=42, 7.5\text{h}$) **`0.34`** | `#285767` ($S=241, 17.2\text{d}$) **`3.38`** | `#287317` ($S=136, 42.6\text{h}$) **`5.14`** |
| **#6** | `#287474` ($S=26, 6.1\text{h}$) **`1.58`** | `#287424` ($S=100, 21.2\text{h}$) **`4.71`** | `#287224` ($S=166, 2.8\text{d}$) **`4.23`** | `#287450` ($S=46, 8.3\text{h}$) **`0.31`** | `#286638` ($S=202, 7.8\text{d}$) **`3.37`** | `#286729` ($S=220, 6.3\text{d}$) **`5.13`** |
| **#7** | `#287527` ($S=16, 3.9\text{h}$) **`1.51`** | `#287474` ($S=26, 6.1\text{h}$) **`4.29`** | `#286916` ($S=193, 5.1\text{d}$) **`4.18`** | `#287474` ($S=26, 6.1\text{h}$) **`0.31`** | `#286916` ($S=193, 5.1\text{d}$) **`3.37`** | `#286916` ($S=193, 5.1\text{d}$) **`5.01`** |
| **#8** | `#287424` ($S=100, 21.2\text{h}$) **`1.44`** | `#287440` ($S=63, 14.8\text{h}$) **`4.27`** | `#286052` ($S=290, 14.2\text{d}$) **`4.15`** | `#287462` ($S=20, 6.9\text{h}$) **`0.21`** | `#286493` ($S=203, 8.4\text{d}$) **`3.37`** | `#287381` ($S=117, 29.3\text{h}$) **`4.86`** |
| **#9** | `#287440` ($S=63, 14.8\text{h}$) **`1.41`** | `#287527` ($S=16, 3.9\text{h}$) **`4.12`** | `#286638` ($S=202, 7.8\text{d}$) **`3.64`** | `#287521` ($S=14, 5.6\text{h}$) **`0.20`** | `#284570` ($S=291, 30.2\text{d}$) **`3.36`** | `#287223` ($S=133, 2.8\text{d}$) **`4.53`** |
| **#10** | `#287547` ($S=8, 2.0\text{h}$) **`1.31`** | `#287547` ($S=8, 2.0\text{h}$) **`3.99`** | `#287317` ($S=136, 42.6\text{h}$) **`3.60`** | `#287469` ($S=17, 6.4\text{h}$) **`0.20`** | `#285548` ($S=242, 20.1\text{d}$) **`3.35`** | `#287424` ($S=100, 21.2\text{h}$) **`4.49`** |

#### Analysis:
- In `all` (All Indexed), F2 successfully balances the all-time highest-scoring post (`#204959`, $S=6{,}049$, #1) alongside active multi-day viral sensations (`#287376`, `#287316`, `#286998`, `#286729`, `#286052`).
- F1 and Raw Velocity completely collapse on `all`, presenting the exact same 4 young posts from today.

---

## 6. Final Production Specification

### 6.1 Formula Definition

The popularity score for post $p$ over window $W$ is:

$$\text{HotScore}(p, W) = \frac{\Delta S(p, W)}{\Delta t(p, W) + \tau_W} \cdot \max(1, S(p))^{0.35}$$

Where:
- $\Delta S(p, W)$ is the score delta inside sliding window $W$:
  - If $p.\text{created\_at} \ge T_{\text{window\_start}}$, then $\Delta S = p.\text{score}$.
  - If $p.\text{created\_at} < T_{\text{window\_start}}$, then $\Delta S = \max(0, p.\text{score} - S_{\text{baseline}})$.
- $\Delta t(p, W)$ is the exposure time inside window $W$ in hours:
  - $\Delta t = \max\left(0.1, \frac{T_{\text{now}} - \max(p.\text{created\_at}, T_{\text{window\_start}})}{3600}\right)$.
- $S(p)$ is the total cumulative score of the post (`p.score`).

### 6.2 Window-Proportional Exposure Damping Schedule ($\tau_W$)

To prevent short-lived bursts from dominating multi-day windows while preserving real-time sensitivity on short windows, $\tau_W$ is defined as:

| Window Code | UI Label | Window Duration | Damping Constant $\tau_W$ | Relative Ratio ($\tau_W / W$) |
|---|---|---|---|---|
| `6h` | Rising | 6 hours | **$1.5\text{ hours}$** | $25.0\%$ |
| `24h` / `day` | Today | 24 hours | **$2.0\text{ hours}$** | $8.3\%$ |
| `3d` | 3 Days | 72 hours | **$6.0\text{ hours}$** | $8.3\%$ |
| `7d` / `week` | Week | 168 hours | **$12.0\text{ hours}$** | $7.1\%$ |
| `14d` | 2 Weeks | 336 hours | **$24.0\text{ hours}$** | $7.1\%$ |
| `30d` / `month` | Month | 720 hours | **$48.0\text{ hours}$** | $6.7\%$ |
| `all` | All Indexed | All Time ($W=0$) | **$168.0\text{ hours}$** | $1\text{ week}$ |

### 6.3 SQLite Query Specification

In `internal/index/index.go`:

```sql
-- Damped velocity (upvotes/hr):
CAST(score_delta AS REAL) / (eff_hours + :tau_W) AS score_rate,

-- Superlinear Momentum HotScore:
(CAST(score_delta AS REAL) / (eff_hours + :tau_W)) * POWER(MAX(1, p.score), 0.35) AS hot_score
```

And in `ORDER BY` for `SortHot`:

```sql
ORDER BY hot_score DESC, score_delta DESC, p.score DESC, p.id DESC
```

### 6.4 UI Popularity Badge (`[eye]` / Flame Badge)

In the current implementation, the card badge renders `hotCount(p.ScoreRate) = max(1, round(score_rate))`. On `hypnoview.db`, this causes almost all posts in `7d`, `30d`, and `all` to display `[eye] 1` (even posts with score 318 or 6,049).

The recommended UI presentation:
- **HotScore Display**: Render `round(p.HotScore)` on the badge. Under F2, `24h` values range cleanly from `22` down to `1`, and `7d` values range from `25` down to `1`.
- **Secondary Rate Tag**: For posts where `p.ScoreRate >= 1.0`, display `+X.X/h` in the card metadata.

### 6.5 Performance & Indexing Analysis

Evaluating `POWER(MAX(1, p.score), 0.35)` incurs negligible overhead in SQLite (`< 0.1 \mu\text{s}` per row). Query latency on the 100,000-post dataset:
- `24h` (124 candidate posts): **2.1 ms**
- `7d` (785 candidate posts): **4.6 ms**
- `30d` (2,707 candidate posts): **12.4 ms**
- `all` (full index scan with score threshold): **< 28 ms**
