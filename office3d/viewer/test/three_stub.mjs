// 測試用的最小 three.js 替身（只提供 agents.js 用到的類別）
export class Vector2 { constructor(x = 0, y = 0) { this.x = x; this.y = y; } set(x, y) { this.x = x; this.y = y; return this; } }
export class Vector3 {
  constructor(x = 0, y = 0, z = 0) { this.x = x; this.y = y; this.z = z; }
  set(x, y, z) { this.x = x; this.y = y; this.z = z; return this; }
  clone() { return new Vector3(this.x, this.y, this.z); }
  copy(v) { this.x = v.x; this.y = v.y; this.z = v.z; return this; }
}
export class Quaternion { clone() { return new Quaternion(); } copy() { return this; } multiply() { return this; } setFromEuler() { return this; } }
export class Euler { set() { return this; } }
