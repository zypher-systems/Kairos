/** Clock face dispatching scheduled work to local, agent, and SSH targets. */
export function LoginScene() {
  return (
    <svg className="login-scene" viewBox="0 0 720 420" role="img" aria-label="Kairos dispatches scheduled work to local, agent, and SSH targets">
      <title>A clock ticks; jobs leave for local, agent, and SSH hosts</title>

      <text className="label" x="96" y="36">
        TIME
      </text>
      <text className="label" x="318" y="36">
        DISPATCH
      </text>
      <text className="label" x="548" y="36">
        TARGETS
      </text>

      <circle className="ring" cx="148" cy="210" r="96" />
      <circle className="ring" cx="148" cy="210" r="68" />
      <circle className="ring ring-soft" cx="148" cy="210" r="40" />

      {Array.from({ length: 12 }, (_, i) => {
        const a = (i / 12) * Math.PI * 2 - Math.PI / 2
        const inner = i % 3 === 0 ? 82 : 88
        return (
          <line
            key={i}
            className={i % 3 === 0 ? 'tick' : 'tick-faint'}
            x1={148 + Math.cos(a) * inner}
            y1={210 + Math.sin(a) * inner}
            x2={148 + Math.cos(a) * 96}
            y2={210 + Math.sin(a) * 96}
          />
        )
      })}

      <g>
        <line className="hand-hour" x1="148" y1="210" x2="148" y2="162" />
        <animateTransform attributeName="transform" type="rotate" from="0 148 210" to="360 148 210" dur="48s" repeatCount="indefinite" />
      </g>
      <g>
        <line className="hand" x1="148" y1="210" x2="148" y2="128" />
        <animateTransform attributeName="transform" type="rotate" from="0 148 210" to="360 148 210" dur="12s" repeatCount="indefinite" />
      </g>
      <circle className="hub" cx="148" cy="210" r="3.5" />

      <path className="wire" id="lane-local" d="M244 176 C 340 176, 420 92, 560 92" />
      <path className="wire" id="lane-agent" d="M244 210 H 560" />
      <path className="wire" id="lane-ssh" d="M244 244 C 340 244, 420 328, 560 328" />

      <circle className="node" cx="560" cy="92" r="3.5" />
      <circle className="node" cx="560" cy="210" r="3.5" />
      <circle className="node" cx="560" cy="328" r="3.5" />

      <text className="label" x="576" y="96">
        LOCAL
      </text>
      <text className="label" x="576" y="214">
        AGENT
      </text>
      <text className="label" x="576" y="332">
        SSH
      </text>

      <circle className="pkt" r="2.6">
        <animateMotion dur="3.6s" repeatCount="indefinite" begin="0s">
          <mpath href="#lane-local" />
        </animateMotion>
      </circle>
      <circle className="pkt" r="2.6">
        <animateMotion dur="4.1s" repeatCount="indefinite" begin="0.9s">
          <mpath href="#lane-agent" />
        </animateMotion>
      </circle>
      <circle className="pkt" r="2.6">
        <animateMotion dur="3.8s" repeatCount="indefinite" begin="1.6s">
          <mpath href="#lane-ssh" />
        </animateMotion>
      </circle>
      <circle className="pkt pkt-dim" r="2.2">
        <animateMotion dur="3.6s" repeatCount="indefinite" begin="1.8s">
          <mpath href="#lane-local" />
        </animateMotion>
      </circle>
    </svg>
  )
}
