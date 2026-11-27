// omixor 標誌:跟形象網站(site/favicon.svg)同一個圖,登入等畫面用。
// 底色透明,只畫圓環與圓點;顏色挑過,淺色與深色佈景下都看得清楚。
export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" role="img" aria-label="omixor">
      <circle cx="16" cy="16" r="10.5" stroke="url(#omixor-logo-ring)" strokeWidth="2" />
      <circle cx="16" cy="16" r="3.6" fill="#1fbf8f" />
      <circle cx="16" cy="5.5" r="1.9" fill="#8e7dff" />
      <circle cx="25.1" cy="21.2" r="1.9" fill="#4cc3ff" />
      <circle cx="6.9" cy="21.2" r="1.9" fill="#ffb547" />
      <defs>
        <linearGradient id="omixor-logo-ring" x1="4" y1="4" x2="28" y2="28" gradientUnits="userSpaceOnUse">
          <stop stopColor="#8e7dff" />
          <stop offset="1" stopColor="#1fbf8f" />
        </linearGradient>
      </defs>
    </svg>
  );
}
