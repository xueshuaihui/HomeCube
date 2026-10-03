<template>
  <view class="login-page">
    <view class="login-header">
      <text class="app-name">家立方 HomeCube</text>
      <text class="app-slogan">记录家的每一份温暖</text>
    </view>
    
    <view class="login-form">
      <view class="input-group">
        <text class="label">手机号</text>
        <input 
          v-model="phone" 
          type="number" 
          placeholder="请输入手机号" 
          maxlength="11"
          class="input-field"
        />
      </view>
      
      <view class="input-group">
        <text class="label">验证码</text>
        <view class="code-input-wrapper">
          <input 
            v-model="smsCode" 
            type="number" 
            placeholder="请输入验证码" 
            maxlength="6"
            class="input-field code-input"
          />
          <button 
            @click="sendSMSCode" 
            :disabled="countdown > 0"
            class="send-code-btn"
          >
            {{ countdown > 0 ? `${countdown}s` : '获取验证码' }}
          </button>
        </view>
      </view>
      
      <button @click="handleLogin" class="login-btn">登录</button>
      
      <view class="tips">
        <text class="tip-text">测试模式：验证码固定为 123456</text>
      </view>
    </view>
  </view>
</template>

<script>
export default {
  data() {
    return {
      phone: '',
      smsCode: '',
      countdown: 0,
      timer: null
    }
  },
  
  methods: {
    async sendSMSCode() {
      if (!this.phone || this.phone.length !== 11) {
        uni.showToast({ title: '请输入正确的手机号', icon: 'none' })
        return
      }
      
      try {
        const res = await uni.request({
          url: '/api/homeos/auth/sms-code',
          method: 'POST',
          data: { phone: this.phone }
        })
        
        if (res.statusCode === 200) {
          uni.showToast({ title: res.data.message, icon: 'success' })
          this.startCountdown()
        } else {
          uni.showToast({ title: '发送失败', icon: 'none' })
        }
      } catch (error) {
        console.error('Send SMS code failed:', error)
        uni.showToast({ title: '网络错误', icon: 'none' })
      }
    },
    
    startCountdown() {
      this.countdown = 60
      this.timer = setInterval(() => {
        this.countdown--
        if (this.countdown <= 0) {
          clearInterval(this.timer)
        }
      }, 1000)
    },
    
    async handleLogin() {
      if (!this.phone || !this.smsCode) {
        uni.showToast({ title: '请填写完整信息', icon: 'none' })
        return
      }
      
      try {
        const res = await uni.request({
          url: '/api/homeos/auth/login',
          method: 'POST',
          data: {
            phone: this.phone,
            sms_code: this.smsCode
          }
        })
        
        if (res.statusCode === 200) {
          // Store tokens
          uni.setStorageSync('access_token', res.data.access_token)
          uni.setStorageSync('refresh_token', res.data.refresh_token)
          uni.setStorageSync('user', res.data.user)
          uni.setStorageSync('families', res.data.families)
          
          // Check if user has families
          if (res.data.families && res.data.families.length > 0) {
            // Navigate to home
            uni.reLaunch({ url: '/pages/homeos/home/index' })
          } else {
            // Navigate to create family page
            uni.redirectTo({ url: '/pages/homeos/family/create' })
          }
        } else {
          uni.showToast({ title: res.data.error || '登录失败', icon: 'none' })
        }
      } catch (error) {
        console.error('Login failed:', error)
        uni.showToast({ title: '网络错误', icon: 'none' })
      }
    }
  },
  
  onUnload() {
    if (this.timer) {
      clearInterval(this.timer)
    }
  }
}
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: 60rpx 40rpx;
}

.login-header {
  text-align: center;
  margin-bottom: 80rpx;
}

.app-name {
  font-size: 48rpx;
  font-weight: bold;
  color: #ffffff;
  display: block;
  margin-bottom: 20rpx;
}

.app-slogan {
  font-size: 28rpx;
  color: rgba(255, 255, 255, 0.8);
}

.login-form {
  background: #ffffff;
  border-radius: 20rpx;
  padding: 40rpx;
  box-shadow: 0 10rpx 40rpx rgba(0, 0, 0, 0.1);
}

.input-group {
  margin-bottom: 30rpx;
}

.label {
  font-size: 28rpx;
  color: #333333;
  margin-bottom: 10rpx;
  display: block;
}

.input-field {
  width: 100%;
  height: 80rpx;
  border: 2rpx solid #e0e0e0;
  border-radius: 10rpx;
  padding: 0 20rpx;
  font-size: 28rpx;
  box-sizing: border-box;
}

.code-input-wrapper {
  display: flex;
  gap: 20rpx;
}

.code-input {
  flex: 1;
}

.send-code-btn {
  width: 200rpx;
  height: 80rpx;
  line-height: 80rpx;
  background: #667eea;
  color: #ffffff;
  border: none;
  border-radius: 10rpx;
  font-size: 26rpx;
}

.send-code-btn[disabled] {
  background: #cccccc;
}

.login-btn {
  width: 100%;
  height: 90rpx;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: #ffffff;
  border: none;
  border-radius: 10rpx;
  font-size: 32rpx;
  font-weight: bold;
  margin-top: 40rpx;
}

.tips {
  margin-top: 30rpx;
  text-align: center;
}

.tip-text {
  font-size: 24rpx;
  color: #999999;
}
</style>
