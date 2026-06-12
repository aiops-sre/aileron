import { useEffect, useRef, useState } from 'react';

interface UseSoundOptions {
  volume?: number;
  enabled?: boolean;
}

interface SoundConfig {
  url: string;
  volume?: number;
}

// Predefined sound URLs (can be replaced with actual audio files)
const SOUNDS = {
  critical: '/sounds/critical-alert.mp3',
  high: '/sounds/high-alert.mp3',
  medium: '/sounds/medium-alert.mp3',
  low: '/sounds/low-alert.mp3',
  success: '/sounds/success.mp3',
  error: '/sounds/error.mp3',
  notification: '/sounds/notification.mp3',
};

export function useSound(soundKey: keyof typeof SOUNDS, options: UseSoundOptions = {}) {
  const [isPlaying, setIsPlaying] = useState(false);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const {
    volume = 0.5,
    enabled = true
  } = options;

  useEffect(() => {
    // Create audio element
    const audio = new Audio(SOUNDS[soundKey]);
    audio.volume = volume;
    audio.preload = 'auto';

    audioRef.current = audio;

    return () => {
      audio.pause();
      audio.src = '';
      audioRef.current = null;
    };
  }, [soundKey, volume]);

  const play = async () => {
    if (!enabled || !audioRef.current) return;

    try {
      setIsPlaying(true);
      audioRef.current.currentTime = 0;
      await audioRef.current.play();
    } catch (error) {
      console.warn('[Sound] Failed to play audio:', error);
    } finally {
      setTimeout(() => setIsPlaying(false), audioRef.current?.duration || 1000);
    }
  };

  const stop = () => {
    if (audioRef.current) {
      audioRef.current.pause();
      audioRef.current.currentTime = 0;
      setIsPlaying(false);
    }
  };

  return { play, stop, isPlaying };
}

// Hook for managing sound settings globally
export function useSoundSettings() {
  const [isSoundEnabled, setIsSoundEnabled] = useState(() => {
    const saved = localStorage.getItem('soundEnabled');
    return saved !== null ? JSON.parse(saved) : true;
  });

  const [volume, setVolume] = useState(() => {
    const saved = localStorage.getItem('soundVolume');
    return saved !== null ? parseFloat(saved) : 0.5;
  });

  useEffect(() => {
    localStorage.setItem('soundEnabled', JSON.stringify(isSoundEnabled));
  }, [isSoundEnabled]);

  useEffect(() => {
    localStorage.setItem('soundVolume', volume.toString());
  }, [volume]);

  const toggleSound = () => setIsSoundEnabled((prev: boolean) => !prev);

  return {
    isSoundEnabled,
    volume,
    setVolume,
    toggleSound,
    setIsSoundEnabled
  };
}

// Helper hook to play alert sounds based on severity
export function useAlertSound() {
  const { isSoundEnabled, volume } = useSoundSettings();
  const criticalSound = useSound('critical', { enabled: isSoundEnabled, volume });
  const highSound = useSound('high', { enabled: isSoundEnabled, volume });
  const mediumSound = useSound('medium', { enabled: isSoundEnabled, volume });
  const lowSound = useSound('low', { enabled: isSoundEnabled, volume });

  const playAlertSound = (severity: 'critical' | 'high' | 'medium' | 'low') => {
    switch (severity) {
      case 'critical':
        criticalSound.play();
        break;
      case 'high':
        highSound.play();
        break;
      case 'medium':
        mediumSound.play();
        break;
      case 'low':
        lowSound.play();
        break;
    }
  };

  return { playAlertSound, isSoundEnabled, volume };
}
