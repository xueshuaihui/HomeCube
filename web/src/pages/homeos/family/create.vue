<template>
  <view class="create-family-page">
    <view class="page-header">
      <text class="title">创建家庭</text>
      <text class="subtitle">开始记录您家庭的温暖时光</text>
    </view>
    
    <view class="form-container">
      <view class="input-group">
        <text class="label">家庭名称</text>
        <input 
          v-model="familyName" 
          placeholder="例如：幸福小家" 
          class="input-field"
        />
      </view>
      
      <view class="input-group">
        <text class="label">时区</text>
        <picker @change="onTimezoneChange" :value="timezoneIndex" :range="timezoneList">
          <view class="picker-field">
            <text>{{ selectedTimezone }}</text>
            <text class="arrow">▼</text>
          </view>
        </picker>
      </view>
      
      <view class="input-group">
        <text class="label">货币</text>
        <picker @change="onCurrencyChange" :value="currencyIndex" :range="currencyList">
          <view class="picker-field">
            <text>{{ selectedCurrency }}</text>
            <text class="arrow">▼</text>
          </view>
        </picker>
      </view>
      
      <button @click="handleCreate" class="create-btn">创建家庭</button>
    </view>
  </view>
</template>

<script>
export default {
  data() {
    return {
      familyName: '',
      timezoneList: ['Asia/Shanghai (UTC+8)', 'America/New_York (UTC-5)', 'Europe/London (UTC+0)'],
      timezoneIndex: 0,
      currencyList: ['CNY (人民币)', 'USD (美元)', 'EUR (欧元)', 'JPY (日元)'],
      currencyIndex: 0
    }
  },
  
  computed: {
    selectedTimezone() {
      return this.timezoneList[this.timezoneIndex]
    },
    selectedCurrency() {
      return this.currencyList[this.currencyIndex]
    }
  },
  
  methods: {
    onTimezoneChange(e) {
      this.timezoneIndex = e.detail.value
    },
    
    onCurrencyChange(e) {
      this.currencyIndex = e.detail.value
    },
    
    async handleCreate() {
      if (!this.familyName.trim()) {
        uni.showToast({ title: '请输入家庭名称', icon: 'none' })
        return
      }
      
      const accessToken = uni.getStorageSync('access_token')
      if (!accessToken) {
        uni.redirectTo({ url: '/pages/homeos/auth/login' })
        return
      }
      
      try {
        const res = await uni.request({
          url: '/api/homeos/families',
          method: 'POST',
          header: {
            'Authorization': `Bearer ${accessToken}`
          },
          data: {
            name: this.familyName,
            timezone: this.selectedTimezone.split(' ')[0],
            currency: this.selectedCurrency.split(' ')[0]
          }
        })
        
        if (res.statusCode === 201) {
          uni.showToast({ title: '创建成功', icon: 'success' })
          // Navigate to module selection page (to be implemented)
          setTimeout(() => {
            uni.reLaunch({ url: '/pages/homeos/home/index' })
          }, 1500)
        } else {
          uni.showToast({ title: res.data.error || '创建失败', icon: 'none' })
        }
      } catch (error) {
        console.error('Create family failed:', error)
        uni.showToast({ title: '网络错误', icon: 'none' })
      }
    }
  }
}
</script>

<style scoped>
.create-family-page {
  min-height: 100vh;
  background: #f5f5f5;
  padding: 40rpx;
}

.page-header {
  text-align: center;
  margin-bottom: 60rpx;
}

.title {
  font-size: 40rpx;
  font-weight: bold;
  color: #333333;
  display: block;
  margin-bottom: 10rpx;
}

.subtitle {
  font-size: 28rpx;
  color: #999999;
}

.form-container {
  background: #ffffff;
  border-radius: 20rpx;
  padding: 40rpx;
  box-shadow: 0 4rpx 20rpx rgba(0, 0, 0, 0.05);
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

.picker-field {
  width: 100%;
  height: 80rpx;
  border: 2rpx solid #e0e0e0;
  border-radius: 10rpx;
  padding: 0 20rpx;
  font-size: 28rpx;
  display: flex;
  justify-content: space-between;
  align-items: center;
  box-sizing: border-box;
}

.arrow {
  font-size: 20rpx;
  color: #999999;
}

.create-btn {
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
</style>
